// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import "errors"

var errRange = errors.New("caduceus/codec: malformed range")

// Type OIDs of the built-in range types.
const (
	Int4Range uint32 = 3904
	NumRange  uint32 = 3906
	TsRange   uint32 = 3908
	TstzRange uint32 = 3910
	DateRange uint32 = 3912
	Int8Range uint32 = 3926
)

// rangeElements maps a range type, and its multirange, to its element type.
// A range of another type, one a user created, has its bounds read and
// written as text.
var rangeElements = map[uint32]uint32{
	Int4Range: Int4, Int8Range: Int8, NumRange: Numeric,
	TsRange: Timestamp, TstzRange: Timestamptz, DateRange: Date,
	4451: Int4, 4536: Int8, 4532: Numeric, 4533: Timestamp, 4534: Timestamptz, 4535: Date,
}

// Range is a value of a range type: int4range, int8range, numrange, tsrange,
// tstzrange, daterange, or one of your own, with T the type of its bounds,
// such as Range[int64] or Range[time.Time]. Valid is false for NULL.
//
// A bound that is Unbounded has no value; an empty range has both bounds
// Empty. What the server sends is canonical: an int4range of [1,5] arrives
// as [1,6).
type Range[T any] struct {
	Lower, Upper         T
	LowerType, UpperType BoundType
	Valid                bool
}

func (r *Range[T]) scanColumn(oid uint32, format int16, src []byte) error {
	*r = Range[T]{}
	if src == nil {
		return nil
	}
	text, err := asString(oid, format, src)
	if err != nil {
		return err
	}
	parsed, err := parseRange(text)
	if err != nil {
		return err
	}
	element := elementOf(oid)
	r.LowerType, r.UpperType, r.Valid = parsed.lowerType, parsed.upperType, true
	if err := scanBound(element, parsed.lower, &r.Lower); err != nil {
		return err
	}
	return scanBound(element, parsed.upper, &r.Upper)
}

func (r Range[T]) encodeColumn(dst []byte, oid uint32) ([]byte, bool, error) {
	if !r.Valid {
		return dst, true, nil
	}
	return appendRange(dst, elementOf(oid), r.LowerType, r.UpperType, r.Lower, r.Upper)
}

// elementOf is a range's element type, Unknown for one not built in.
func elementOf(oid uint32) uint32 {
	if element, ok := rangeElements[oid]; ok {
		return element
	}
	return Unknown
}

// scanBound scans a bound's text, if it has one, into dest.
func scanBound(element uint32, text *string, dest any) error {
	if text == nil {
		return nil
	}
	return Scan(element, 0, []byte(*text), dest)
}
