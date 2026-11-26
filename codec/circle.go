// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// Circle is a value of circle, a centre and a radius. Valid is false for
// NULL.
type Circle struct {
	P     Point
	R     float64
	Valid bool
}

func (c *Circle) scanColumn(oid uint32, format int16, src []byte) error {
	*c = Circle{}
	if src == nil {
		return nil
	}
	numbers, err := geometryNumbers(oid, format, src, 3)
	if err != nil {
		return err
	}
	*c = Circle{P: Point{X: numbers[0], Y: numbers[1], Valid: true}, R: numbers[2], Valid: true}
	return nil
}

func (c Circle) encodeColumn(dst []byte, _ uint32) ([]byte, bool, error) {
	if !c.Valid {
		return dst, true, nil
	}
	dst = c.P.append(append(dst, '<'))
	return append(appendNumber(append(dst, ','), c.R), '>'), false, nil
}
