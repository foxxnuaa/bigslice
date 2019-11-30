// Copyright 2018 GRAIL, Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package slicetype

import (
	"reflect"
	"testing"
)

var (
	typeOfString = reflect.TypeOf("")
	typeOfInt    = reflect.TypeOf(0)
)

func TestType(t *testing.T) {
	types := []reflect.Type{typeOfString, typeOfInt, typeOfString}
	typ := New(types...)
	if got, want := Columns(typ), types; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if !Assignable(typ, typ) {
		t.Error("types should be assignable to themselves")
	}
}

func TestSignature(t *testing.T) {
	arg := New(typeOfString, typeOfInt)
	ret := New()
	if got, want := Signature(arg, ret), "func(string, int)"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	ret = New(typeOfInt)
	if got, want := Signature(arg, ret), "func(string, int) int"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	ret = New(typeOfInt, typeOfString)
	if got, want := Signature(arg, ret), "func(string, int) (int, string)"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}
