// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// Line is a value of line, the infinite line Ax + By + C = 0. Valid is false
// for NULL.
type Line struct {
	A, B, C float64
	Valid   bool
}

func (l *Line) scanColumn(oid uint32, format int16, src []byte) error {
	*l = Line{}
	if src == nil {
		return nil
	}
	numbers, err := geometryNumbers(oid, format, src, 3)
	if err != nil {
		return err
	}
	*l = Line{A: numbers[0], B: numbers[1], C: numbers[2], Valid: true}
	return nil
}

func (l Line) encodeColumn(dst []byte, _ uint32) ([]byte, bool, error) {
	if !l.Valid {
		return dst, true, nil
	}
	dst = appendNumber(append(dst, '{'), l.A)
	dst = appendNumber(append(dst, ','), l.B)
	return append(appendNumber(append(dst, ','), l.C), '}'), false, nil
}
