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

// numericInBinary asks for numeric in binary and everything else as the codec
// would, as a caller with its own numeric decoder does.
func numericInBinary(oid uint32) int16 {
	if oid == codec.Numeric {
		return wire.BinaryFormat
	}
	return codec.ResultFormat(oid)
}

func TestLiveBinaryResultsAndBinaryParam(t *testing.T) {
	for _, options := range []Options{
		{ResultFormat: numericInBinary},
		{ResultFormat: numericInBinary, StatementCacheCapacity: -1},
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
