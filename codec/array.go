// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

var errArray = errors.New("caduceus/codec: malformed binary array")

// maxArrayDims is PostgreSQL's own limit, MAXDIM.
const maxArrayDims = 6

// binaryArray is a decoded array header: its shape and where its elements are.
type binaryArray struct {
	element  uint32
	dims     []int
	elements [][]byte
}

func parseArray(src []byte) (binaryArray, error) {
	if len(src) < 12 {
		return binaryArray{}, errArray
	}
	ndim := int(be32(src))
	if ndim < 0 || ndim > maxArrayDims || len(src) < 12+8*ndim {
		return binaryArray{}, errArray
	}
	array := binaryArray{element: uint32(be32(src[8:]))}
	total := 1
	rest := src[12:]
	for range ndim {
		size := int(be32(rest))
		if size < 0 || size > len(src) {
			return binaryArray{}, errArray
		}
		array.dims = append(array.dims, size)
		total *= size
		rest = rest[8:]
	}
	if ndim == 0 {
		total = 0
	}
	if total > len(rest)/4 {
		return binaryArray{}, errArray
	}
	array.elements = make([][]byte, total)
	for i := range total {
		if len(rest) < 4 {
			return binaryArray{}, errArray
		}
		length := int(be32(rest))
		rest = rest[4:]
		if length < 0 {
			continue
		}
		if length > len(rest) {
			return binaryArray{}, errArray
		}
		array.elements[i] = rest[:length:length]
		rest = rest[length:]
	}
	return array, nil
}

// elementFormat is the format the elements of a binary array are in: binary,
// since the array is, which for a text-like element is its text anyway.
const elementFormat = 1

func naturalArray(element uint32, src []byte) (any, error) {
	array, err := parseArray(src)
	if err != nil {
		return nil, err
	}
	values := make([]any, len(array.elements))
	for i, raw := range array.elements {
		if values[i], err = Natural(element, elementFormat, raw); err != nil {
			return nil, err
		}
	}
	return nest(values, array.dims), nil
}

// nest shapes a flat list into nested []any by the array's dimensions.
func nest(values []any, dims []int) any {
	if len(dims) <= 1 {
		return values
	}
	stride := len(values) / max(dims[0], 1)
	out := make([]any, dims[0])
	for i := range out {
		out[i] = nest(values[i*stride:(i+1)*stride], dims[1:])
	}
	return out
}

// scanArray decodes a binary array into a slice, or nested slices for more
// than one dimension.
//
// An array in text, as one of a type with no binary form here arrives, an
// enum's, numeric's or inet's, is read from its literal, and each element
// scanned from its text, as its element type if that is known here and as a
// value of unknown type is otherwise: an int, a float, a string or anything
// with an UnmarshalText.
func scanArray(oid uint32, format int16, src []byte, value reflect.Value) error {
	element, known := arrayElements[oid]
	array, err := readArray(oid, known, format, src, value)
	if err != nil {
		return err
	}
	if !known {
		element = Unknown
	}
	if len(array.dims) == 0 {
		value.Set(reflect.MakeSlice(value.Type(), 0, 0))
		return nil
	}
	_, err = fillSlice(value, element, format, array.dims, array.elements)
	return err
}

// readArray reads a binary array of a known type, or an array literal.
func readArray(oid uint32, known bool, format int16, src []byte, value reflect.Value) (binaryArray, error) {
	switch {
	case format == 1 && known:
		return parseArray(src)
	case format == 0 && len(src) > 0 && (src[0] == '{' || src[0] == '['):
		return parseTextArray(src)
	}
	return binaryArray{}, errFormat(oid, value.Type().String())
}

func fillSlice(value reflect.Value, element uint32, format int16, dims []int, elements [][]byte) ([][]byte, error) {
	if value.Kind() != reflect.Slice {
		return nil, fmt.Errorf("caduceus/codec: array has more dimensions than %s", value.Type())
	}
	slice := reflect.MakeSlice(value.Type(), dims[0], dims[0])
	for i := range dims[0] {
		target := slice.Index(i)
		if len(dims) > 1 {
			var err error
			if elements, err = fillSlice(target, element, format, dims[1:], elements); err != nil {
				return nil, err
			}
			continue
		}
		if err := Scan(element, format, elements[0], target.Addr().Interface()); err != nil {
			return nil, err
		}
		elements = elements[1:]
	}
	value.Set(slice)
	return elements, nil
}

// arrayText renders a binary array as PostgreSQL's array literal, for a caller
// that asked for a string or a Scanner that parses one.
func arrayText(oid uint32, src []byte) (string, error) {
	array, err := parseArray(src)
	if err != nil {
		return "", err
	}
	if len(array.dims) == 0 {
		return "{}", nil
	}
	texts := make([]string, len(array.elements))
	for i, raw := range array.elements {
		if raw == nil {
			texts[i] = "NULL"
			continue
		}
		text, err := printedElement(arrayElements[oid], raw)
		if err != nil {
			return "", err
		}
		texts[i] = quoteArrayElement(text)
	}
	var b strings.Builder
	writeNested(&b, texts, array.dims)
	return b.String(), nil
}

// printedElement is the text array_out prints for an element. A bytea's is
// its hex form, where a bytea column read as a string is its bytes.
func printedElement(element uint32, raw []byte) (string, error) {
	if element == Bytea {
		return `\x` + hexString(raw), nil
	}
	return asString(element, elementFormat, raw)
}

func writeNested(b *strings.Builder, texts []string, dims []int) {
	b.WriteByte('{')
	stride := len(texts) / max(dims[0], 1)
	for i := range dims[0] {
		if i > 0 {
			b.WriteByte(',')
		}
		if len(dims) > 1 {
			writeNested(b, texts[i*stride:(i+1)*stride], dims[1:])
		} else {
			b.WriteString(texts[i])
		}
	}
	b.WriteByte('}')
}

// quoteArrayElement quotes an element the way array_out does: when it is empty,
// is the word NULL, or contains anything that would break the literal.
func quoteArrayElement(text string) string {
	if text != "" && !strings.EqualFold(text, "NULL") && !strings.ContainsAny(text, "{},\"\\ \t\n\r\v\f") {
		return text
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(text) + `"`
}
