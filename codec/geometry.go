// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import (
	"errors"
	"strconv"
	"strings"
)

var errGeometry = errors.New("caduceus/codec: malformed geometric value")

// Type OIDs of the geometric types.
const (
	PointOID   uint32 = 600
	LsegOID    uint32 = 601
	PathOID    uint32 = 602
	BoxOID     uint32 = 603
	PolygonOID uint32 = 604
	LineOID    uint32 = 628
	CircleOID  uint32 = 718
)

// geometryNumbers reads the numbers of a geometric literal, whatever brackets
// group them: (1,2), ((1,2),(3,4)), <(1,2),3>, {1,2,3}. want is how many it
// must have, or -1 for an even number of at least two, the points of a path
// or a polygon.
func geometryNumbers(oid uint32, format int16, src []byte, want int) ([]float64, error) {
	text, err := asString(oid, format, src)
	if err != nil {
		return nil, err
	}
	fields := strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune("()[]{}<>, ", r) })
	numbers := make([]float64, len(fields))
	for i, field := range fields {
		if numbers[i], err = strconv.ParseFloat(field, 64); err != nil {
			return nil, errGeometry
		}
	}
	if (want >= 0 && len(numbers) != want) || (want < 0 && (len(numbers) < 2 || len(numbers)%2 != 0)) {
		return nil, errGeometry
	}
	return numbers, nil
}

// pointsOf pairs numbers into points.
func pointsOf(numbers []float64) []Point {
	points := make([]Point, len(numbers)/2)
	for i := range points {
		points[i] = Point{X: numbers[2*i], Y: numbers[2*i+1], Valid: true}
	}
	return points
}

// appendNumber writes a float as the geometric types' input reads it.
func appendNumber(dst []byte, f float64) []byte {
	return strconv.AppendFloat(dst, f, 'g', -1, 64)
}

// appendPoints writes points as (x,y),(x,y), between open and end.
func appendPoints(dst []byte, open, end byte, points []Point) []byte {
	dst = append(dst, open)
	for i, p := range points {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = p.append(dst)
	}
	return append(dst, end)
}
