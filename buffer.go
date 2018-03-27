// Copyright 2018 GRAIL, Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package bigslice

import "reflect"

// TaskBuffer is an in-memory buffer of task output. It has the
// ability to handle multiple partitions, and stores vectors of
// records for efficiency.
//
// TaskBuffer layout is: partition, slices, columns.
type taskBuffer [][][]reflect.Value

// Slice returns column vectors for the provided partition and global
// offset. The returned offset indicates the position of the global
// offset into the returned vectors. A returned offset of -1
// indicates EOF. Slice is designed to perform zero-copy reads
// from a taskBuffer.
//
// TODO(marius): Slicing is currently inefficient as it requires a
// linear walk through the stored vectors. We should aggregate
// lengths so that we can perform a binary search. Alternatively, we
// can return a cookie from Slice that enables efficient resumption.
func (b taskBuffer) Slice(partition, off int) ([]reflect.Value, int) {
	var beg, end int
	if partition == AllPartitions {
		beg, end = 0, len(b)
	} else {
		beg, end = partition, partition+1
	}
	// Find the offset.
	var n int
	for i := beg; i < end; i++ {
		for _, cols := range b[i] {
			l := cols[0].Len()
			if n+l > off {
				return cols, off - n
			}
			n += l
		}
	}
	return nil, -1
}

type taskBufferReader struct {
	q       taskBuffer
	i, j, k int
}

func (r *taskBufferReader) Read(out ...reflect.Value) (int, error) {
loop:
	for {
		switch {
		case len(r.q) == r.i:
			return 0, EOF
		case len(r.q[r.i]) == r.j:
			r.i++
			r.j, r.k = 0, 0
		case r.q[r.i][r.j][0].Len() == r.k:
			r.j++
			r.k = 0
		default:
			break loop
		}
	}
	buf := r.q[r.i][r.j]
	n := out[0].Len()
	if m := buf[0].Len() - r.k; m < n {
		n = m
	}
	l := r.k + n
	for i, val := range out {
		// TODO(marius): Consider changing the Reader interface to allow
		// for zero-copy transfers in this case.
		reflect.Copy(val, r.q[r.i][r.j][i].Slice(r.k, l))
	}
	r.k = l
	return n, nil
}

// Reader returns a Reader for a partition of the taskBuffer.
func (b taskBuffer) Reader(partition int) Reader {
	if partition == AllPartitions {
		return &taskBufferReader{q: b}
	}
	return &taskBufferReader{q: b[partition : partition+1]}
}