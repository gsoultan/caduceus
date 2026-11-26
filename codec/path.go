// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import "strings"

// Path is a value of path: points joined in order, Closed if the last joins
// the first. Valid is false for NULL.
type Path struct {
	P      []Point
	Closed bool
	Valid  bool
}

func (p *Path) scanColumn(oid uint32, format int16, src []byte) error {
	*p = Path{}
	if src == nil {
		return nil
	}
	numbers, err := geometryNumbers(oid, format, src, -1)
	if err != nil {
		return err
	}
	// An open path prints in brackets, a closed one in parentheses.
	*p = Path{P: pointsOf(numbers), Closed: !strings.HasPrefix(strings.TrimSpace(string(src)), "["), Valid: true}
	return nil
}

func (p Path) encodeColumn(dst []byte, _ uint32) ([]byte, bool, error) {
	if !p.Valid {
		return dst, true, nil
	}
	if p.Closed {
		return appendPoints(dst, '(', ')', p.P), false, nil
	}
	return appendPoints(dst, '[', ']', p.P), false, nil
}
