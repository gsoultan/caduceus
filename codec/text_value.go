// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import (
	"math"
	"strconv"
)

// EncodeText appends arg as text for a column of type oid, the form COPY's text
// format and array literals want. It reports null for NULL.
func EncodeText(dst []byte, oid uint32, arg any) ([]byte, bool, error) {
	if out, ok := appendTextFast(dst, oid, arg); ok {
		return out, false, nil
	}
	start := len(dst)
	encoded, format, null, err := Encode(dst, oid, arg)
	if err != nil || null || format == 0 {
		return encoded, null, err
	}
	raw := append([]byte(nil), encoded[start:]...)
	dst = encoded[:start]
	if oid == Bytea {
		return append(append(dst, `\x`...), hexString(raw)...), false, nil
	}
	text, err := asString(oid, 1, raw)
	return append(dst, text...), false, err
}

// appendTextFast writes the commonest column values straight as text, which is
// what every row of a COPY spends its time on. It reports false for anything
// it does not handle, including an integer out of its column's range, which
// the general path then reports as an overflow.
func appendTextFast(dst []byte, oid uint32, arg any) ([]byte, bool) {
	switch v := arg.(type) {
	case string:
		if isTextual(oid) {
			return append(dst, v...), true
		}
	case int64:
		return appendIntText(dst, oid, v)
	case int:
		return appendIntText(dst, oid, int64(v))
	case int32:
		return appendIntText(dst, oid, int64(v))
	case float64:
		if oid == Float8 {
			return strconv.AppendFloat(dst, v, 'g', -1, 64), true
		}
	case bool:
		if oid == Bool {
			if v {
				return append(dst, 't'), true
			}
			return append(dst, 'f'), true
		}
	}
	return dst, false
}

func appendIntText(dst []byte, oid uint32, v int64) ([]byte, bool) {
	switch {
	case oid == Int8, oid == Numeric,
		oid == Int4 && v >= math.MinInt32 && v <= math.MaxInt32,
		oid == Int2 && v >= math.MinInt16 && v <= math.MaxInt16:
		return strconv.AppendInt(dst, v, 10), true
	}
	return dst, false
}

func isTextual(oid uint32) bool {
	switch oid {
	case Text, Varchar, Bpchar, Name:
		return true
	}
	return false
}
