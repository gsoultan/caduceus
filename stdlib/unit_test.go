// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib

import (
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/gsoultan/caduceus"
	"github.com/gsoultan/caduceus/codec"
	"github.com/gsoultan/caduceus/wire"
)

func TestTxOptions(t *testing.T) {
	tests := []struct {
		level    sql.IsolationLevel
		readOnly bool
		want     caduceus.TxOptions
		refused  bool
	}{
		{level: sql.LevelDefault, want: caduceus.TxOptions{}},
		{level: sql.LevelReadUncommitted, want: caduceus.TxOptions{IsoLevel: caduceus.ReadUncommitted}},
		{level: sql.LevelReadCommitted, want: caduceus.TxOptions{IsoLevel: caduceus.ReadCommitted}},
		{level: sql.LevelRepeatableRead, readOnly: true, want: caduceus.TxOptions{IsoLevel: caduceus.RepeatableRead, ReadOnly: true}},
		{level: sql.LevelSerializable, want: caduceus.TxOptions{IsoLevel: caduceus.Serializable}},
		{level: sql.LevelSnapshot, refused: true},
		{level: sql.LevelWriteCommitted, refused: true},
		{level: sql.LevelLinearizable, refused: true},
	}
	for _, tt := range tests {
		got, err := txOptions(driver.TxOptions{Isolation: driver.IsolationLevel(tt.level), ReadOnly: tt.readOnly})
		if tt.refused != errors.Is(err, ErrIsolationLevel) {
			t.Errorf("txOptions(%v) error = %v, refused %v", tt.level, err, tt.refused)
		}
		if got != tt.want {
			t.Errorf("txOptions(%v) = %+v, want %+v", tt.level, got, tt.want)
		}
	}
}

func TestCheckArg(t *testing.T) {
	var out string
	tests := []struct {
		arg  driver.NamedValue
		want error
	}{
		{arg: driver.NamedValue{Ordinal: 1, Value: int64(1)}},
		// Passed through to caduceus rather than refused by database/sql's
		// default converter, which knows no slices.
		{arg: driver.NamedValue{Ordinal: 1, Value: []int32{1, 2}}},
		{arg: driver.NamedValue{Ordinal: 1, Value: caduceus.NamedArgs{"id": 1}}},
		{arg: driver.NamedValue{Ordinal: 1, Name: "id", Value: 1}, want: ErrNamedArg},
		{arg: driver.NamedValue{Ordinal: 1, Value: sql.Out{Dest: &out}}, want: ErrOutArg},
	}
	for _, tt := range tests {
		if err := checkArg(&tt.arg); err != tt.want {
			t.Errorf("checkArg(%+v) = %v, want %v", tt.arg, err, tt.want)
		}
	}
}

func TestColumnValue(t *testing.T) {
	be := func(n uint64, size int) []byte {
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, n)
		return b[8-size:]
	}
	minus7 := int32(-7)
	tests := []struct {
		name   string
		oid    uint32
		format int16
		raw    []byte
		want   driver.Value
	}{
		{"null", codec.Int4, 1, nil, nil},
		// An int2 or int4 is an int64, as database/sql documents a
		// driver.Value; caduceus's own Values gives int16 and int32.
		{"int2", codec.Int2, 1, be(7, 2), int64(7)},
		{"int4", codec.Int4, 1, be(uint64(uint32(minus7)), 4), int64(-7)},
		{"int8", codec.Int8, 1, be(1<<40, 8), int64(1 << 40)},
		{"oid", codec.OID, 1, be(math.MaxUint32, 4), int64(math.MaxUint32)},
		{"float4", codec.Float4, 1, be(uint64(math.Float32bits(1.5)), 4), float64(1.5)},
		{"float8", codec.Float8, 1, be(math.Float64bits(2.25), 8), 2.25},
		{"bool", codec.Bool, 1, []byte{1}, true},
		{"bytea", codec.Bytea, 1, []byte{0, 1, 2}, []byte{0, 1, 2}},
		{"uuid", codec.UUID, 1, []byte{0xa0, 0xee, 0xbc, 0x99, 0x9c, 0x0b, 0x4e, 0xf8, 0xbb, 0x6d, 0x6b, 0xb9, 0xbd, 0x38, 0x0a, 0x11}, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"},
		{"numeric text", codec.Numeric, 0, []byte("12.50"), "12.50"},
		{"jsonb", codec.JSONB, 1, []byte("\x01{\"a\": 1}"), "{\"a\": 1}"},
		{"date", codec.Date, 1, be(0, 4), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"infinite timestamp", codec.Timestamp, 1, be(math.MaxInt64, 8), "infinity"},
	}
	for _, tt := range tests {
		got, err := columnValue(&wire.Field{Name: tt.name, DataTypeOID: tt.oid, Format: tt.format}, tt.raw)
		if err != nil {
			t.Errorf("%s: columnValue() error = %v", tt.name, err)
			continue
		}
		if !equal(got, tt.want) {
			t.Errorf("%s: columnValue() = %#v, want %#v", tt.name, got, tt.want)
		}
	}
}

func equal(got, want driver.Value) bool {
	switch w := want.(type) {
	case []byte:
		g, ok := got.([]byte)
		return ok && string(g) == string(w)
	case time.Time:
		g, ok := got.(time.Time)
		return ok && g.Equal(w)
	}
	return got == want
}

func TestColumnValueNamesTheColumn(t *testing.T) {
	_, err := columnValue(&wire.Field{Name: "n", DataTypeOID: codec.Int4, Format: 1}, []byte{1})
	if err == nil {
		t.Fatal("columnValue() of a 1-byte int4 = nil error")
	}
	if want := "caduceus/stdlib: column \"n\": "; len(err.Error()) < len(want) || err.Error()[:len(want)] != want {
		t.Errorf("columnValue() error = %q, want it to start %q", err, want)
	}
}

func TestLengthAndPrecision(t *testing.T) {
	tests := []struct {
		oid          uint32
		modifier     int32
		length       int64
		hasLength    bool
		precision    int64
		scale        int64
		hasPrecision bool
	}{
		{oid: codec.Varchar, modifier: 4 + 20, length: 20, hasLength: true},
		{oid: codec.Varchar, modifier: -1, length: math.MaxInt64, hasLength: true},
		{oid: codec.Bpchar, modifier: 4 + 1, length: 1, hasLength: true},
		{oid: codec.Text, modifier: -1, length: math.MaxInt64, hasLength: true},
		{oid: codec.Int4, modifier: -1},
		{oid: codec.Numeric, modifier: 4 + (10<<16 | 2), precision: 10, scale: 2, hasPrecision: true},
		// numeric(3, -2), which PostgreSQL 15 allows, stores the scale in
		// 11 signed bits.
		{oid: codec.Numeric, modifier: 4 + (3<<16 | 0x7fe), precision: 3, scale: -2, hasPrecision: true},
		{oid: codec.Numeric, modifier: -1},
	}
	for _, tt := range tests {
		field := &wire.Field{DataTypeOID: tt.oid, TypeModifier: tt.modifier}
		if n, ok := length(field); n != tt.length || ok != tt.hasLength {
			t.Errorf("length(%d, %d) = %d, %v; want %d, %v", tt.oid, tt.modifier, n, ok, tt.length, tt.hasLength)
		}
		p, s, ok := precisionScale(field)
		if p != tt.precision || s != tt.scale || ok != tt.hasPrecision {
			t.Errorf("precisionScale(%d, %d) = %d, %d, %v; want %d, %d, %v", tt.oid, tt.modifier, p, s, ok, tt.precision, tt.scale, tt.hasPrecision)
		}
	}
}

func TestResult(t *testing.T) {
	r := result{tag: "UPDATE 3"}
	if n, err := r.RowsAffected(); n != 3 || err != nil {
		t.Errorf("RowsAffected() = %d, %v", n, err)
	}
	if _, err := r.LastInsertId(); err != ErrNoLastInsertID {
		t.Errorf("LastInsertId() error = %v", err)
	}
}

func TestRegistered(t *testing.T) {
	if !slices.Contains(sql.Drivers(), DriverName) {
		t.Fatalf("sql.Drivers() = %v, want %q among them", sql.Drivers(), DriverName)
	}
	if _, err := sql.Open(DriverName, "postgres://host/db?sslmode=nonsense"); err == nil {
		t.Error("sql.Open() of a bad connection string = nil error, want the parse error up front")
	}
}
