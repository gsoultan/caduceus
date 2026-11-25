// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import "errors"

// ErrNoBinary is returned by EncodeBinary for a value the codec can only write
// as text, which the server would have to parse: a string for an integer
// column, say. The value is not at fault; the caller sends it another way.
var ErrNoBinary = errors.New("caduceus/codec: value has no binary form for its type")

// BinaryInput reports whether EncodeBinary can write values of type oid: the
// types the codec encodes in binary, and those whose binary form is their text.
func BinaryInput(oid uint32) bool {
	return binaryScalars[oid] || textIsBinary(oid) || oid == JSONB
}

// EncodeBinary appends arg in the binary format of type oid, the form COPY's
// binary format wants, and reports null for NULL. It never parses: a value
// that would need the server's input function is refused with ErrNoBinary and
// nothing is appended.
func EncodeBinary(dst []byte, oid uint32, arg any) ([]byte, bool, error) {
	start := len(dst)
	if oid == JSONB {
		dst = append(dst, jsonbVersion)
	}
	out, format, null, err := Encode(dst, oid, arg)
	switch {
	case err != nil, null:
		return out[:start], null, err
	case format == 1, textIsBinary(oid), oid == JSONB:
		return out, false, nil
	}
	return out[:start], false, ErrNoBinary
}

// jsonbVersion leads a jsonb in binary; what follows it is the JSON text.
const jsonbVersion = 1

// textIsBinary reports whether a type's binary form is the bytes of its text.
// name and "char" are left out: their receive functions refuse a value their
// input functions would have truncated.
func textIsBinary(oid uint32) bool {
	switch oid {
	case Text, Varchar, Bpchar, JSON:
		return true
	}
	return false
}
