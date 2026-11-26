// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// Lseg is a value of lseg, a line segment between two points. Valid is false
// for NULL.
type Lseg struct {
	P     [2]Point
	Valid bool
}

func (l *Lseg) scanColumn(oid uint32, format int16, src []byte) error {
	*l = Lseg{}
	if src == nil {
		return nil
	}
	numbers, err := geometryNumbers(oid, format, src, 4)
	if err != nil {
		return err
	}
	points := pointsOf(numbers)
	*l = Lseg{P: [2]Point{points[0], points[1]}, Valid: true}
	return nil
}

func (l Lseg) encodeColumn(dst []byte, _ uint32) ([]byte, bool, error) {
	if !l.Valid {
		return dst, true, nil
	}
	return appendPoints(dst, '[', ']', l.P[:]), false, nil
}
