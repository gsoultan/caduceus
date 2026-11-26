// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import (
	"bytes"
	"errors"
)

var errTextArray = errors.New("caduceus/codec: malformed array literal")

// textArray reads an array literal, as array_out prints one: {1,2,NULL},
// {"a b","say \"hi\""}, {{1,2},{3,4}}, with an optional [1:2]= in front for
// bounds other than 1. It is how an array of a type with no binary form here
// arrives: an enum's, numeric's, inet's.
type textArray struct {
	src      []byte
	pos      int
	dims     []int
	elements [][]byte
	// leaf is the depth the elements are at, -1 until the first is read.
	leaf int
}

// parseTextArray reads src into the shape and elements of a binary array,
// each element its text, nil for NULL.
func parseTextArray(src []byte) (binaryArray, error) {
	a := textArray{src: src, leaf: -1}
	if src[0] == '[' {
		equals := bytes.IndexByte(src, '=')
		if equals < 0 {
			return binaryArray{}, errTextArray
		}
		a.pos = equals + 1
	}
	if err := a.level(0); err != nil {
		return binaryArray{}, err
	}
	if a.skipSpace(); a.pos != len(a.src) {
		return binaryArray{}, errTextArray
	}
	if len(a.elements) == 0 {
		return binaryArray{}, nil
	}
	return binaryArray{dims: a.dims, elements: a.elements}, nil
}

// level reads one {...} at depth, and checks it has as many items as the
// others at that depth: an array is rectangular.
func (a *textArray) level(depth int) error {
	if depth >= maxArrayDims || !a.take('{') {
		return errTextArray
	}
	if a.skipSpace(); a.take('}') {
		return a.count(depth, 0)
	}
	items := 0
	for {
		if err := a.item(depth); err != nil {
			return err
		}
		items++
		a.skipSpace()
		if a.take('}') {
			return a.count(depth, items)
		}
		if !a.take(',') {
			return errTextArray
		}
	}
}

// count records how many items a level at depth had, or checks it against
// the first such level's. An inner level ends before the one around it, so
// the depths are filled in from the inside out.
func (a *textArray) count(depth, items int) error {
	for len(a.dims) <= depth {
		a.dims = append(a.dims, -1)
	}
	switch a.dims[depth] {
	case -1:
		a.dims[depth] = items
		return nil
	case items:
		return nil
	}
	return errTextArray
}

// item reads one element, or a nested level. Every element is at the same
// depth, the deepest: one level cannot hold arrays and elements both.
func (a *textArray) item(depth int) error {
	a.skipSpace()
	if a.pos >= len(a.src) {
		return errTextArray
	}
	if a.src[a.pos] == '{' {
		return a.level(depth + 1)
	}
	switch {
	case a.leaf < 0:
		a.leaf = depth
	case a.leaf != depth:
		return errTextArray
	}
	if a.src[a.pos] == '"' {
		return a.quoted()
	}
	return a.unquoted()
}

// quoted reads a double-quoted element, in which a backslash escapes the
// next character.
func (a *textArray) quoted() error {
	a.pos++
	value := []byte{}
	for a.pos < len(a.src) {
		c := a.src[a.pos]
		a.pos++
		switch {
		case c == '"':
			a.elements = append(a.elements, value)
			return nil
		case c == '\\' && a.pos < len(a.src):
			value = append(value, a.src[a.pos])
			a.pos++
		default:
			value = append(value, c)
		}
	}
	return errTextArray
}

// unquoted reads an element up to the next comma or closing brace, with
// surrounding space trimmed; NULL, in any case, is a NULL.
func (a *textArray) unquoted() error {
	value := []byte{}
	for a.pos < len(a.src) && a.src[a.pos] != ',' && a.src[a.pos] != '}' {
		c := a.src[a.pos]
		a.pos++
		if c == '\\' && a.pos < len(a.src) {
			c = a.src[a.pos]
			a.pos++
		}
		value = append(value, c)
	}
	value = bytes.TrimSpace(value)
	switch {
	case len(value) == 0:
		return errTextArray
	case bytes.EqualFold(value, []byte("NULL")):
		a.elements = append(a.elements, nil)
	default:
		a.elements = append(a.elements, value)
	}
	return nil
}

func (a *textArray) take(c byte) bool {
	if a.pos < len(a.src) && a.src[a.pos] == c {
		a.pos++
		return true
	}
	return false
}

func (a *textArray) skipSpace() {
	for a.pos < len(a.src) && (a.src[a.pos] == ' ' || a.src[a.pos] == '\t' || a.src[a.pos] == '\n' || a.src[a.pos] == '\r') {
		a.pos++
	}
}
