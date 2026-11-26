// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import "strings"

// rangeText is a range literal taken apart: each bound's type, and its text,
// nil for an unbounded or empty one.
type rangeText struct {
	lowerType, upperType BoundType
	lower, upper         *string
}

// parseRange reads a range literal as range_out prints one: empty, [1,5),
// (,5], ["2024-01-01 00:00:00","2024-02-01 00:00:00").
func parseRange(text string) (rangeText, error) {
	text = strings.TrimSpace(text)
	if strings.EqualFold(text, "empty") {
		return rangeText{lowerType: Empty, upperType: Empty}, nil
	}
	if len(text) < 3 {
		return rangeText{}, errRange
	}
	var r rangeText
	switch text[0] {
	case '[':
		r.lowerType = Inclusive
	case '(':
		r.lowerType = Exclusive
	default:
		return rangeText{}, errRange
	}
	switch text[len(text)-1] {
	case ']':
		r.upperType = Inclusive
	case ')':
		r.upperType = Exclusive
	default:
		return rangeText{}, errRange
	}
	lower, rest, err := rangeBound(text[1 : len(text)-1])
	if err != nil || !strings.HasPrefix(rest, ",") {
		return rangeText{}, errRange
	}
	upper, rest, err := rangeBound(rest[1:])
	if err != nil || rest != "" {
		return rangeText{}, errRange
	}
	r.lower, r.upper = lower, upper
	if lower == nil {
		r.lowerType = Unbounded
	}
	if upper == nil {
		r.upperType = Unbounded
	}
	return r, nil
}

// rangeBound reads one bound up to the comma or the end: nothing for an
// unbounded one, a double-quoted one with backslash escapes and "" for a
// quote, or the characters as they are.
func rangeBound(text string) (*string, string, error) {
	if text == "" || text[0] == ',' {
		return nil, text, nil
	}
	if text[0] != '"' {
		end := strings.IndexByte(text, ',')
		if end < 0 {
			end = len(text)
		}
		bound := text[:end]
		return &bound, text[end:], nil
	}
	var bound strings.Builder
	for i := 1; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '\\' && i+1 < len(text):
			i++
			bound.WriteByte(text[i])
		case c == '"' && i+1 < len(text) && text[i+1] == '"':
			i++
			bound.WriteByte('"')
		case c == '"':
			value := bound.String()
			return &value, text[i+1:], nil
		default:
			bound.WriteByte(c)
		}
	}
	return nil, "", errRange
}

// appendRange writes a range literal, each bound quoted, which any element
// type's text survives.
func appendRange(dst []byte, element uint32, lowerType, upperType BoundType, lower, upper any) ([]byte, bool, error) {
	if lowerType == Empty || upperType == Empty {
		return append(dst, "empty"...), false, nil
	}
	dst = append(dst, "(["[boolIndex(lowerType == Inclusive)])
	dst, err := appendBound(dst, element, lowerType, lower)
	if err != nil {
		return dst, false, err
	}
	dst = append(dst, ',')
	if dst, err = appendBound(dst, element, upperType, upper); err != nil {
		return dst, false, err
	}
	return append(dst, ")]"[boolIndex(upperType == Inclusive)]), false, nil
}

// appendBound writes one bound, quoted, or nothing for an unbounded one.
func appendBound(dst []byte, element uint32, bound BoundType, value any) ([]byte, error) {
	if bound == Unbounded {
		return dst, nil
	}
	text, null, err := EncodeText(nil, element, value)
	if err != nil {
		return dst, err
	}
	if null {
		return dst, nil
	}
	return appendQuoted(dst, text), nil
}

// appendQuoted writes text double-quoted, with a backslash before each quote
// and backslash, as range, record and hstore literals read it.
func appendQuoted(dst, text []byte) []byte {
	dst = append(dst, '"')
	for _, c := range text {
		if c == '"' || c == '\\' {
			dst = append(dst, '\\')
		}
		dst = append(dst, c)
	}
	return append(dst, '"')
}

func boolIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}
