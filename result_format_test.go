// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"bytes"
	"errors"
	"testing"

	"github.com/gsoultan/caduceus/codec"
)

// allBinary asks for every column in binary, as a caller that decodes
// RawValues itself would.
func allBinary(uint32) int16 { return 1 }

func TestLiveResultFormatOption(t *testing.T) {
	for _, cache := range []int{0, -1} {
		conn := liveConn(t, Options{StatementCacheCapacity: cache, ResultFormat: allBinary})
		ctx := t.Context()

		rows, err := conn.Query(ctx, "SELECT $1::numeric, $2::jsonb, $3::text", "1.5", `{"a":1}`, "x")
		if err != nil {
			t.Fatalf("cache %d: Query() = %v", cache, err)
		}
		if !rows.Next() {
			t.Fatalf("cache %d: no row: %v", cache, rows.Err())
		}
		for i, field := range rows.Fields() {
			if field.Format != 1 {
				t.Errorf("cache %d: column %d arrived in format %d, want binary", cache, i, field.Format)
			}
		}
		raw := rows.RawValues()
		// numeric 1.5 in binary: 2 digits, weight 0, positive, scale 1, 1 and 5000.
		want := []byte{0, 2, 0, 0, 0, 0, 0, 1, 0, 1, 0x13, 0x88}
		if !bytes.Equal(raw[0], want) {
			t.Errorf("cache %d: numeric = %x, want %x", cache, raw[0], want)
		}
		if len(raw[1]) == 0 || raw[1][0] != 1 {
			t.Errorf("cache %d: jsonb = %x, want the version byte 1 first", cache, raw[1])
		}
		var text string
		var number any
		err = rows.Scan(&number, nil, &text)
		var unreadable *codec.TypeError
		if err != nil && !errors.As(err, &unreadable) {
			t.Errorf("cache %d: Scan() = %v, want nil or a *codec.TypeError", cache, err)
		}
		if errors.Is(err, ErrResultChanged) {
			t.Errorf("cache %d: Scan() = %v, which blames a changed result for the format the caller chose", cache, err)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("cache %d: Err() = %v", cache, err)
		}

		var s string
		if err := conn.QueryRow(ctx, "SELECT 'still text'::text").Scan(&s); err != nil || s != "still text" {
			t.Errorf("cache %d: text column in binary = %q, %v", cache, s, err)
		}
	}
}

func TestLiveResultFormatDefault(t *testing.T) {
	conn := liveConn(t, Options{})
	rows, err := conn.Query(t.Context(), "SELECT 1.5::numeric")
	if err != nil {
		t.Fatalf("Query() = %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no row: %v", rows.Err())
	}
	if f := rows.Fields()[0].Format; f != 0 {
		t.Errorf("numeric arrived in format %d without the option, want text", f)
	}
}
