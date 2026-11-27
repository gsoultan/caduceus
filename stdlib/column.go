// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib

import (
	"database/sql/driver"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/gsoultan/caduceus/codec"
	"github.com/gsoultan/caduceus/wire"
)

// columnValue decodes one column into one of the types database/sql
// documents for a driver.Value. Everything without such a type of its own is
// its text, which every one of them has.
func columnValue(field *wire.Field, raw []byte) (driver.Value, error) {
	if raw == nil {
		return nil, nil
	}
	value, err := decode(field, raw)
	if err != nil {
		return nil, fmt.Errorf("caduceus/stdlib: column %q: %w", field.Name, err)
	}
	return value, nil
}

// decode is columnValue for a column that is not NULL.
func decode(field *wire.Field, raw []byte) (driver.Value, error) {
	oid, format := field.DataTypeOID, field.Format
	switch scanType(oid) {
	case int64Type:
		var n int64
		err := codec.Scan(oid, format, raw, &n)
		return n, err
	case float64Type:
		var f float64
		err := codec.Scan(oid, format, raw, &f)
		return f, err
	case boolType:
		var b bool
		err := codec.Scan(oid, format, raw, &b)
		return b, err
	case bytesType:
		var b []byte
		err := codec.Scan(oid, format, raw, &b)
		return b, err
	case timeType:
		// infinity and -infinity, which no time.Time holds, come back as
		// their text.
		return codec.Natural(oid, format, raw)
	}
	var s string
	err := codec.Scan(oid, format, raw, &s)
	return s, err
}

var (
	int64Type   = reflect.TypeFor[int64]()
	float64Type = reflect.TypeFor[float64]()
	boolType    = reflect.TypeFor[bool]()
	bytesType   = reflect.TypeFor[[]byte]()
	timeType    = reflect.TypeFor[time.Time]()
	stringType  = reflect.TypeFor[string]()
)

// scanTypes are the types with a driver.Value of their own; every other
// column is a string. It is never written after initialisation.
var scanTypes = map[uint32]reflect.Type{
	codec.Int2: int64Type, codec.Int4: int64Type, codec.Int8: int64Type, codec.OID: int64Type,
	codec.Float4: float64Type, codec.Float8: float64Type,
	codec.Bool:  boolType,
	codec.Bytea: bytesType,
	codec.Date:  timeType, codec.Timestamp: timeType, codec.Timestamptz: timeType,
}

// scanType is the Go type a column of type oid is given.
func scanType(oid uint32) reflect.Type {
	if t, ok := scanTypes[oid]; ok {
		return t
	}
	return stringType
}

// typeNames are PostgreSQL's names for the types the driver knows. It is
// never written after initialisation.
var typeNames = map[uint32]string{
	codec.Bool: "BOOL", codec.Bytea: "BYTEA", codec.Char: "CHAR", codec.Name: "NAME",
	codec.Int8: "INT8", codec.Int2: "INT2", codec.Int4: "INT4", codec.Text: "TEXT",
	codec.OID: "OID", codec.JSON: "JSON", codec.Float4: "FLOAT4", codec.Float8: "FLOAT8",
	codec.Unknown: "UNKNOWN", codec.Bpchar: "BPCHAR", codec.Varchar: "VARCHAR",
	codec.Date: "DATE", codec.Time: "TIME", codec.Timestamp: "TIMESTAMP",
	codec.Timestamptz: "TIMESTAMPTZ", codec.Interval: "INTERVAL", codec.Numeric: "NUMERIC",
	codec.UUID: "UUID", codec.JSONB: "JSONB",
	1000: "_BOOL", 1001: "_BYTEA", 1005: "_INT2", 1007: "_INT4", 1016: "_INT8",
	1028: "_OID", 1021: "_FLOAT4", 1022: "_FLOAT8", 1182: "_DATE", 1183: "_TIME",
	1115: "_TIMESTAMP", 1185: "_TIMESTAMPTZ", 1187: "_INTERVAL", 2951: "_UUID",
	1009: "_TEXT", 1015: "_VARCHAR", 1014: "_BPCHAR", 1003: "_NAME", 199: "_JSON",
	3807: "_JSONB", 1231: "_NUMERIC",
}

// varlenaHeader is what PostgreSQL adds to a declared length or precision in
// a type modifier.
const varlenaHeader = 4

// length is ColumnTypeLength.
func length(field *wire.Field) (int64, bool) {
	switch field.DataTypeOID {
	case codec.Text, codec.Bytea:
		return math.MaxInt64, true
	case codec.Varchar, codec.Bpchar:
		return declaredLength(field.TypeModifier)
	}
	return 0, false
}

// declaredLength is the n of varchar(n), which is unlimited without one.
func declaredLength(modifier int32) (int64, bool) {
	if modifier < varlenaHeader {
		return math.MaxInt64, true
	}
	return int64(modifier - varlenaHeader), true
}

// precisionScale is ColumnTypePrecisionScale. A numeric declared without a
// precision has none to report.
func precisionScale(field *wire.Field) (int64, int64, bool) {
	if field.DataTypeOID != codec.Numeric || field.TypeModifier < varlenaHeader {
		return 0, 0, false
	}
	// The scale is 11 bits, signed since PostgreSQL 15 allowed it negative.
	modifier := field.TypeModifier - varlenaHeader
	return int64(modifier >> 16 & 0xffff), int64((modifier&0x7ff)^0x400) - 0x400, true
}
