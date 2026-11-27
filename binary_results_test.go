// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/gsoultan/caduceus/codec"
	"github.com/gsoultan/caduceus/wire"
)

// int4Param writes itself as an int4 in binary, and refuses any other type.
type int4Param int32

func (p int4Param) AppendBinaryParam(dst []byte, oid uint32) ([]byte, bool, error) {
	if oid != codec.Int4 {
		return dst, false, errors.New("int4Param: not an int4")
	}
	return binary.BigEndian.AppendUint32(dst, uint32(p)), false, nil
}

func TestNewStatementAsksBinaryForTheTypesNamed(t *testing.T) {
	fields := []wire.Field{{Name: "n", DataTypeOID: codec.Numeric}, {Name: "t", DataTypeOID: codec.Text}}
	plain := newStatement("", "", nil, slicesClone(fields), nil)
	if plain.requested(0) != wire.TextFormat {
		t.Fatalf("numeric without BinaryResults asked in %d, want text", plain.requested(0))
	}
	forced := newStatement("", "", nil, slicesClone(fields), Options{BinaryResults: []uint32{codec.Numeric}}.binaryResults())
	if forced.requested(0) != wire.BinaryFormat || forced.requested(1) != wire.TextFormat {
		t.Fatalf("formats = %v, want numeric binary and text text", forced.formats)
	}
}

func slicesClone(f []wire.Field) []wire.Field { return append([]wire.Field(nil), f...) }

func TestLiveBinaryResultsAndBinaryParam(t *testing.T) {
	for _, options := range []Options{
		{BinaryResults: []uint32{codec.Numeric}},
		{BinaryResults: []uint32{codec.Numeric}, StatementCacheCapacity: -1},
	} {
		conn := liveConn(t, options)
		ctx := t.Context()

		rows, err := conn.Query(ctx, "SELECT 1.5::numeric, $1::int4 + 1", int4Param(41))
		if err != nil {
			t.Fatalf("Query = %v", err)
		}
		if !rows.Next() {
			t.Fatalf("no row: %v", rows.Err())
		}
		if rows.Fields()[0].Format != wire.BinaryFormat {
			t.Fatalf("numeric arrived in format %d, want binary", rows.Fields()[0].Format)
		}
		raw := rows.RawValues()
		// 1.5 in numeric's binary form: 2 digits, weight 0, positive, scale 1, 1 and 5000.
		want := []byte{0, 2, 0, 0, 0, 0, 0, 1, 0, 1, 0x13, 0x88}
		if !bytes.Equal(raw[0], want) {
			t.Fatalf("numeric bytes = %v, want %v", raw[0], want)
		}
		if got := int32(binary.BigEndian.Uint32(raw[1])); got != 42 {
			t.Fatalf("int4 = %d, want 42", got)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("Err = %v", err)
		}

		var null *int4Param
		var isNull bool
		if err := conn.QueryRow(ctx, "SELECT $1::int4 IS NULL", null).Scan(&isNull); err != nil || !isNull {
			t.Fatalf("nil BinaryParam: IS NULL = %v, %v; want true", isNull, err)
		}
	}
}

// The slice fast path against a server: each array goes in binary and comes
// back as the server read it.
func TestLiveSliceParameters(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	id := [16]byte{0xa0, 0xee, 0xbc, 0x99, 0x9c, 0x0b, 0x4e, 0xf8, 0xbb, 0x6d, 0x6b, 0xb9, 0xbd, 0x38, 0x0a, 0x11}
	var got string
	err := conn.QueryRow(ctx, "SELECT $1::int8[]::text || $2::int4[]::text || $3::float8[]::text || $4::bool[]::text || $5::uuid[]::text || $6::text[]::text || $7::varchar[]::text",
		[]int64{1, -2}, []int32{3}, []float64{1.5}, []bool{true, false}, [][16]byte{id}, []string{"a", `b "c"`, ""}, []string{"x"}).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{1,-2}{3}{1.5}{t,f}{a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11}{a,"b \"c\"",""}{x}`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	var n int64
	if err := conn.QueryRow(ctx, "SELECT cardinality($1::int8[])", []int64{}).Scan(&n); err != nil || n != 0 {
		t.Errorf("an empty int8[] read as %d elements: %v", n, err)
	}
}
