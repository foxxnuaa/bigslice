// Copyright 2018 GRAIL, Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

// Package sortio provides facilities for sorting slice outputs
// and merging and reducing sorted record streams.
package sortio

import (
	"container/heap"
	"context"
	"math"
	"reflect"

	"github.com/grailbio/bigslice/frame"
	"github.com/grailbio/bigslice/kernel"
	"github.com/grailbio/bigslice/sliceio"
	"github.com/grailbio/bigslice/slicetype"
)

// SortReader sorts a Reader using the provided Sorter. SortReader
// may spill to disk, in which case it targets spill file sizes of
// spillTarget (in bytes). Because the encoded size of objects is not
// known in advance, sortReader uses a "canary" batch size of ~16k
// rows in order to estimate the size of future reads. The estimate
// is revisited on every subsequent fill and adjusted if it is
// violated by more than 5%.
func SortReader(ctx context.Context, sorter kernel.Sorter, spillTarget int, typ slicetype.Type, r sliceio.Reader) (sliceio.Reader, error) {
	spill, err := sliceio.NewSpiller()
	if err != nil {
		return nil, err
	}
	defer spill.Cleanup()
	f := frame.Make(typ, 1<<14)
	for {
		n, err := sliceio.ReadFull(ctx, r, f)
		if err != nil && err != sliceio.EOF {
			return nil, err
		}
		eof := err == sliceio.EOF
		g := f.Slice(0, n)
		sorter.Sort(g)
		size, err := spill.Spill(g)
		if err != nil {
			return nil, err
		}
		if eof {
			break
		}
		bytesPerRow := size / n
		targetRows := spillTarget / bytesPerRow
		if targetRows < sliceio.SpillBatchSize {
			targetRows = sliceio.SpillBatchSize
		}
		// If we're within 5%, that's ok.
		if math.Abs(float64(f.Len()-targetRows)/float64(targetRows)) > 0.05 {
			if targetRows <= f.Cap() {
				f = f.Slice(0, targetRows)
			} else {
				f = frame.Make(typ, targetRows)
			}
		}
	}
	readers, err := spill.Readers()
	if err != nil {
		return nil, err
	}
	return NewMergeReader(ctx, typ, sorter, readers)
}

// A FrameBuffer is a buffered frame. The frame is filled from
// a reader, and maintains a current offset and length.
type FrameBuffer struct {
	frame.Frame
	sliceio.Reader
	Off, Len int
	Index    int
	N        int
}

// Fill (re-) fills the FrameBuffer when it's empty. An error
// is returned if the underlying reader returns an error.
// EOF is returned if no more data are available.
func (f *FrameBuffer) Fill(ctx context.Context) error {
	if f.Frame.Len() < f.Frame.Cap() {
		f.Frame = f.Frame.Slice(0, f.Frame.Cap())
	}
	var err error
	f.Len, err = f.Reader.Read(ctx, f.Frame)
	f.N++
	if err != nil && err != sliceio.EOF {
		return err
	}
	if err == sliceio.EOF && f.Len > 0 {
		err = nil
	}
	f.Off = 0
	if f.Len == 0 && err == nil {
		err = sliceio.EOF
	}
	return err
}

// FrameBufferHeap implements a heap of FrameBuffers,
// ordered by the provided sorter.
type FrameBufferHeap struct {
	Buffers []*FrameBuffer
	Sorter  kernel.Sorter
}

func (f *FrameBufferHeap) Len() int { return len(f.Buffers) }
func (f *FrameBufferHeap) Less(i, j int) bool {
	return f.Sorter.Less(f.Buffers[i].Frame, f.Buffers[i].Off, f.Buffers[j].Frame, f.Buffers[j].Off)
}
func (f *FrameBufferHeap) Swap(i, j int) {
	f.Buffers[i], f.Buffers[j] = f.Buffers[j], f.Buffers[i]
}

// Push pushes a FrameBuffer onto the heap.
func (f *FrameBufferHeap) Push(x interface{}) {
	buf := x.(*FrameBuffer)
	buf.Index = len(f.Buffers)
	f.Buffers = append(f.Buffers, buf)
}

// Pop removes the FrameBuffer with the smallest priority
// from the heap.
func (f *FrameBufferHeap) Pop() interface{} {
	n := len(f.Buffers)
	elem := f.Buffers[n-1]
	f.Buffers = f.Buffers[:n-1]
	return elem
}

// MergeReader merges multiple (sorted) readers into a
// single sorted reader.
type mergeReader struct {
	err  error
	heap *FrameBufferHeap
}

// NewMergeReader returns a new Reader that is sorted
// according to the provided Sorter. The readers to be merged
// must already be sorted according to the same.
func NewMergeReader(ctx context.Context, typ slicetype.Type, sorter kernel.Sorter, readers []sliceio.Reader) (sliceio.Reader, error) {
	h := new(FrameBufferHeap)
	h.Sorter = sorter
	h.Buffers = make([]*FrameBuffer, 0, len(readers))
	for i := range readers {
		fr := &FrameBuffer{
			Reader: readers[i],
			Frame:  frame.Make(typ, sliceio.SpillBatchSize),
		}
		switch err := fr.Fill(ctx); {
		case err == sliceio.EOF:
			// No data. Skip.
		case err != nil:
			return nil, err
		default:
			h.Buffers = append(h.Buffers, fr)
		}
	}
	heap.Init(h)
	return &mergeReader{heap: h}, nil
}

// Read implements Reader.
func (m *mergeReader) Read(ctx context.Context, out frame.Frame) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	var (
		row = make([]reflect.Value, len(out))
		n   int
		max = out.Len()
	)
	for n < max && len(m.heap.Buffers) > 0 {
		m.heap.Buffers[0].CopyIndex(row, m.heap.Buffers[0].Off)
		out.SetIndex(row, n)
		n++
		m.heap.Buffers[0].Off++
		if m.heap.Buffers[0].Off == m.heap.Buffers[0].Len {
			if err := m.heap.Buffers[0].Fill(ctx); err != nil && err != sliceio.EOF {
				m.err = err
				return 0, err
			} else if err == sliceio.EOF {
				heap.Remove(m.heap, 0)
			} else {
				heap.Fix(m.heap, 0)
			}
		} else {
			heap.Fix(m.heap, 0)
		}
	}
	if n == 0 {
		m.err = sliceio.EOF
	}
	return n, m.err
}