// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// Polygon is a value of polygon, its vertices in order. Valid is false for
// NULL.
type Polygon struct {
	P     []Point
	Valid bool
}

func (p *Polygon) scanColumn(oid uint32, format int16, src []byte) error {
	*p = Polygon{}
	if src == nil {
		return nil
	}
	numbers, err := geometryNumbers(oid, format, src, -1)
	if err != nil {
		return err
	}
	*p = Polygon{P: pointsOf(numbers), Valid: true}
	return nil
}

func (p Polygon) encodeColumn(dst []byte, _ uint32) ([]byte, bool, error) {
	if !p.Valid {
		return dst, true, nil
	}
	return appendPoints(dst, '(', ')', p.P), false, nil
}
