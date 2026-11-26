// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// Point is a value of point, (x,y). Valid is false for NULL.
type Point struct {
	X, Y  float64
	Valid bool
}

func (p *Point) scanColumn(oid uint32, format int16, src []byte) error {
	*p = Point{}
	if src == nil {
		return nil
	}
	numbers, err := geometryNumbers(oid, format, src, 2)
	if err != nil {
		return err
	}
	*p = pointsOf(numbers)[0]
	return nil
}

func (p Point) encodeColumn(dst []byte, _ uint32) ([]byte, bool, error) {
	if !p.Valid {
		return dst, true, nil
	}
	return p.append(dst), false, nil
}

func (p Point) append(dst []byte) []byte {
	dst = appendNumber(append(dst, '('), p.X)
	return append(appendNumber(append(dst, ','), p.Y), ')')
}
