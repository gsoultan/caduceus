// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import (
	"encoding/binary"
	"math"
)

// encodeSlice writes the common slices as binary arrays without reflection:
// int8[], int4[], float8[], bool[], uuid[] and text[] or varchar[] from the Go
// slice of their element. The general path reflects on every element and
// boxes it, which for a list of a few hundred ids handed to = ANY($1) is most
// of the cost of sending it. It reports false for any other pairing, which
// then takes that path.
func encodeSlice(dst []byte, oid uint32, arg any) ([]byte, int16, bool, bool, error) {
	switch v := arg.(type) {
	case []int64:
		if oid != 1016 {
			return dst, 0, false, false, nil
		}
		if v == nil {
			return dst, 1, true, true, nil
		}
		dst = arrayHeader(dst, Int8, len(v))
		for _, n := range v {
			dst = binary.BigEndian.AppendUint32(dst, 8)
			dst = binary.BigEndian.AppendUint64(dst, uint64(n))
		}
	case []int32:
		if oid != 1007 {
			return dst, 0, false, false, nil
		}
		if v == nil {
			return dst, 1, true, true, nil
		}
		dst = arrayHeader(dst, Int4, len(v))
		for _, n := range v {
			dst = binary.BigEndian.AppendUint32(dst, 4)
			dst = binary.BigEndian.AppendUint32(dst, uint32(n))
		}
	case []float64:
		if oid != 1022 {
			return dst, 0, false, false, nil
		}
		if v == nil {
			return dst, 1, true, true, nil
		}
		dst = arrayHeader(dst, Float8, len(v))
		for _, f := range v {
			dst = binary.BigEndian.AppendUint32(dst, 8)
			dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(f))
		}
	case []bool:
		if oid != 1000 {
			return dst, 0, false, false, nil
		}
		if v == nil {
			return dst, 1, true, true, nil
		}
		dst = arrayHeader(dst, Bool, len(v))
		for _, b := range v {
			dst = binary.BigEndian.AppendUint32(dst, 1)
			if b {
				dst = append(dst, 1)
			} else {
				dst = append(dst, 0)
			}
		}
	case [][16]byte:
		if oid != 2951 {
			return dst, 0, false, false, nil
		}
		if v == nil {
			return dst, 1, true, true, nil
		}
		dst = arrayHeader(dst, UUID, len(v))
		for i := range v {
			dst = binary.BigEndian.AppendUint32(dst, 16)
			dst = append(dst, v[i][:]...)
		}
	case []string:
		// A text element's binary form is its bytes, which the server checks
		// against the encoding as it would a literal.
		element := Text
		switch oid {
		case 1009:
		case 1015:
			element = Varchar
		default:
			return dst, 0, false, false, nil
		}
		if v == nil {
			return dst, 1, true, true, nil
		}
		dst = arrayHeader(dst, element, len(v))
		for _, s := range v {
			dst = binary.BigEndian.AppendUint32(dst, uint32(len(s)))
			dst = append(dst, s...)
		}
	default:
		return dst, 0, false, false, nil
	}
	return dst, 1, false, true, nil
}

// arrayHeader is a one-dimensional binary array's header with no NULLs, or an
// empty array's, which has no dimensions.
func arrayHeader(dst []byte, element uint32, n int) []byte {
	if n == 0 {
		dst = binary.BigEndian.AppendUint32(dst, 0)
		dst = binary.BigEndian.AppendUint32(dst, 0)
		return binary.BigEndian.AppendUint32(dst, element)
	}
	dst = binary.BigEndian.AppendUint32(dst, 1) // dimensions
	dst = binary.BigEndian.AppendUint32(dst, 0) // no NULLs
	dst = binary.BigEndian.AppendUint32(dst, element)
	dst = binary.BigEndian.AppendUint32(dst, uint32(n))
	return binary.BigEndian.AppendUint32(dst, 1) // lower bound
}
