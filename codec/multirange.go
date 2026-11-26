// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import "strings"

// Multirange is a value of a multirange type, PostgreSQL 14 and later:
// int4multirange, tstzmultirange and the rest, the ranges in the order the
// server keeps them. Nil is NULL, and an empty, non-nil one is {}.
type Multirange[T any] []Range[T]

func (m *Multirange[T]) scanColumn(oid uint32, format int16, src []byte) error {
	if src == nil {
		*m = nil
		return nil
	}
	text, err := asString(oid, format, src)
	if err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if len(text) < 2 || text[0] != '{' || text[len(text)-1] != '}' {
		return errRange
	}
	ranges := Multirange[T]{}
	element := elementOf(oid)
	for _, literal := range splitRanges(text[1 : len(text)-1]) {
		parsed, err := parseRange(literal)
		if err != nil {
			return err
		}
		r := Range[T]{LowerType: parsed.lowerType, UpperType: parsed.upperType, Valid: true}
		if err := scanBound(element, parsed.lower, &r.Lower); err != nil {
			return err
		}
		if err := scanBound(element, parsed.upper, &r.Upper); err != nil {
			return err
		}
		ranges = append(ranges, r)
	}
	*m = ranges
	return nil
}

func (m Multirange[T]) encodeColumn(dst []byte, oid uint32) ([]byte, bool, error) {
	if m == nil {
		return dst, true, nil
	}
	dst = append(dst, '{')
	for i, r := range m {
		if i > 0 {
			dst = append(dst, ',')
		}
		var err error
		if dst, _, err = appendRange(dst, elementOf(oid), r.LowerType, r.UpperType, r.Lower, r.Upper); err != nil {
			return dst, false, err
		}
	}
	return append(dst, '}'), false, nil
}

// splitRanges splits a multirange's ranges at the commas between them, not
// those inside one or inside a quoted bound.
func splitRanges(text string) []string {
	var ranges []string
	start, inside, quoted := 0, false, false
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case quoted && c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '[' || c == '(':
			inside = true
		case c == ']' || c == ')':
			inside = false
		case c == ',' && !inside:
			ranges = append(ranges, strings.TrimSpace(text[start:i]))
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(text[start:]); rest != "" {
		ranges = append(ranges, rest)
	}
	return ranges
}
