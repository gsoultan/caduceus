// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// Box is a value of box, given by two opposite corners; the server keeps the
// upper right first. Valid is false for NULL.
type Box struct {
	P     [2]Point
	Valid bool
}

func (b *Box) scanColumn(oid uint32, format int16, src []byte) error {
	*b = Box{}
	if src == nil {
		return nil
	}
	numbers, err := geometryNumbers(oid, format, src, 4)
	if err != nil {
		return err
	}
	points := pointsOf(numbers)
	*b = Box{P: [2]Point{points[0], points[1]}, Valid: true}
	return nil
}

func (b Box) encodeColumn(dst []byte, _ uint32) ([]byte, bool, error) {
	if !b.Valid {
		return dst, true, nil
	}
	return appendPoints(dst, '(', ')', b.P[:]), false, nil
}
