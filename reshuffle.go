// Copyright 2019 GRAIL, Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package bigslice

import (
	"fmt"

	"github.com/grailbio/bigslice/slicefunc"
	"github.com/grailbio/bigslice/sliceio"
	"github.com/grailbio/bigslice/typecheck"
)

type reshuffleSlice struct {
	name Name
	Slice
}

// Reshuffle returns a slice that shuffles rows by prefix so that
// all rows with equal prefix values end up in the same shard.
// Rows are not sorted within a shard.
//
// The output slice has the same type as the input.
//
// TODO: Add ReshuffleSort, which also sorts keys within each shard.
func Reshuffle(slice Slice) Slice {
	if err := canMakeCombiningFrame(slice); err != nil {
		typecheck.Panic(1, err.Error())
	}
	return &reshuffleSlice{makeName("reshuffle"), slice}
}

func (r *reshuffleSlice) Name() Name             { return r.name }
func (*reshuffleSlice) NumDep() int              { return 1 }
func (r *reshuffleSlice) Dep(i int) Dep          { return Dep{r.Slice, true, nil, false} }
func (*reshuffleSlice) Combiner() slicefunc.Func { return slicefunc.Nil }

func (r *reshuffleSlice) Reader(shard int, deps []sliceio.Reader) sliceio.Reader {
	if len(deps) != 1 {
		panic(fmt.Errorf("expected one dep, got %d", len(deps)))
	}
	return deps[0]
}
