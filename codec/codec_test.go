// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// Encode and Scan are the two halves the server sits between. Each case encodes a
// value for a type, in whatever format Encode picks, and scans those bytes back
// with that format, which is what the server would return for a column of the
// type in that format. The integration suite checks the same against a real
// server; this is the half that runs without one.
func TestEncodeScanRoundTrip(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, 8, 8, 12, 34, 56, 789012000, time.UTC)
	tests := []struct {
		name string
		oid  uint32
		arg  any
		dest any
		want any
	}{
		{"int2", Int2, int16(-7), new(int16), int16(-7)},
		{"int4 from int", Int4, 123456, new(int32), int32(123456)},
		{"int8 extremes", Int8, int64(math.MinInt64), new(int64), int64(math.MinInt64)},
		{"float4", Float4, float32(0.25), new(float32), float32(0.25)},
		{"float8", Float8, math.E, new(float64), math.E},
		{"bool", Bool, true, new(bool), true},
		{"text", Text, "ünïcode", new(string), "ünïcode"},
		{"bytea", Bytea, []byte{0, 0xff, 7}, new([]byte), []byte{0, 0xff, 7}},
		{"timestamptz", Timestamptz, when, new(time.Time), when},
		{"date", Date, time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC), new(time.Time), time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC)},
		{"interval", Interval, 36*time.Hour + time.Microsecond, new(time.Duration), 36*time.Hour + time.Microsecond},
		{"numeric as text", Numeric, "1.50", new(string), "1.50"},
		{"nullable string", Text, "x", new(nullString), nullString{String: "x", Valid: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			encoded, format, null, err := Encode(nil, tt.oid, tt.arg)
			if err != nil || null {
				t.Fatalf("Encode() = %v, null %v", err, null)
			}
			// A binary parameter is only valid if the result for the type would
			// come back binary too, or the two halves describe different bytes.
			if format == binaryFormat && ResultFormat(tt.oid) != binaryFormat {
				t.Fatalf("Encode() chose binary for %d, which results return as text", tt.oid)
			}
			if err := Scan(tt.oid, format, encoded, tt.dest); err != nil {
				t.Fatalf("Scan() = %v", err)
			}
			got := reflect.ValueOf(tt.dest).Elem().Interface()
			if g, ok := got.(time.Time); ok {
				if !g.Equal(tt.want.(time.Time)) {
					t.Errorf("got %v, want %v", g, tt.want)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

const binaryFormat = int16(1)

// A value that does not fit its destination must fail rather than wrap: an int8
// of 2^40 scanned into an int32 would otherwise come back as a different number.
func TestScanRefusesOverflow(t *testing.T) {
	t.Parallel()

	encoded, format, _, err := Encode(nil, Int8, int64(1)<<40)
	if err != nil {
		t.Fatal(err)
	}
	var small int32
	if err := Scan(Int8, format, encoded, &small); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Scan(2^40 into int32) = %v, want ErrOverflow", err)
	}
}

// NULL into a destination that cannot represent it is an error, not a zero.
func TestScanNullIntoPlainValue(t *testing.T) {
	t.Parallel()

	var n int
	if err := Scan(Int4, binaryFormat, nil, &n); !errors.Is(err, ErrNull) {
		t.Fatalf("Scan(NULL into *int) = %v, want ErrNull", err)
	}
	var p *int
	if err := Scan(Int4, binaryFormat, nil, &p); err != nil || p != nil {
		t.Fatalf("Scan(NULL into **int) = %v, %v", p, err)
	}
}

// Truncated binary input comes from the network and must be an error, never a
// panic in the caller's goroutine.
func FuzzScan(f *testing.F) {
	f.Add(uint32(Int8), []byte{0, 0, 0, 0, 0, 0, 0, 1})
	f.Add(uint32(Interval), []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0})
	f.Add(uint32(Int4)+1000, []byte{0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 23})
	f.Fuzz(func(t *testing.T, oid uint32, src []byte) {
		for _, dest := range []any{new(any), new(string), new(int64), new(time.Time), new([]int64), new([]string)} {
			_ = Scan(oid, binaryFormat, src, dest)
		}
	})
}

// nullString is a Scanner of the shape applications write for a nullable
// column, standing in for database/sql's NullString, which caduceus does not
// import.
type nullString struct {
	String string
	Valid  bool
}

func (n *nullString) Scan(src any) error {
	n.String, n.Valid = "", src != nil
	if s, ok := src.(string); ok {
		n.String = s
	}
	return nil
}

// The text fast path that COPY rows take must write what the general path
// writes, and leave out-of-range integers to it so they still fail as
// overflows rather than reaching the server.
func TestEncodeTextFastPathMatchesTheGeneralPath(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		oid  uint32
		arg  any
		want string
	}{
		{Int8, int64(-42), "-42"}, {Int4, 7, "7"}, {Int2, int32(-3), "-3"},
		{Numeric, int64(10), "10"}, {Float8, 0.25, "0.25"},
		{Bool, true, "t"}, {Bool, false, "f"}, {Text, "a\tb", "a\tb"}, {Varchar, "", ""},
	} {
		got, null, err := EncodeText(nil, tt.oid, tt.arg)
		if err != nil || null || string(got) != tt.want {
			t.Errorf("EncodeText(%d, %v) = %q, %v, %v; want %q", tt.oid, tt.arg, got, null, err, tt.want)
		}
	}
	if _, _, err := EncodeText(nil, Int2, 1<<20); !errors.Is(err, ErrOverflow) {
		t.Errorf("EncodeText(int2, 1<<20) = %v, want ErrOverflow", err)
	}
}

// The decimal fast path that numeric columns take must give exactly what
// strconv gives, and leave to it whatever it cannot do exactly.
func TestParseDecimalFastMatchesStrconv(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"0", "-0", "1.5", "-123.45", "0.1", "0.3", "123456789012345",
		"0.000000000000000000001", "99999.99999", "+7", "1e5", "1234567890123456", ".5", "5.", "-", ".", "", "1.2.3", "0.0000000000000000000000001"} {
		want, wantErr := strconv.ParseFloat(text, 64)
		got, ok := parseDecimalFast([]byte(text))
		if !ok {
			continue
		}
		if wantErr != nil || got != want || math.Signbit(got) != math.Signbit(want) {
			t.Errorf("parseDecimalFast(%q) = %v, strconv = %v, %v", text, got, want, wantErr)
		}
	}
}

func FuzzParseDecimalFast(f *testing.F) {
	for _, seed := range []string{"1.5", "-0.25", "123456789.123456"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		got, ok := parseDecimalFast([]byte(text))
		if !ok {
			return
		}
		want, err := strconv.ParseFloat(text, 64)
		if err != nil || got != want {
			t.Fatalf("parseDecimalFast(%q) = %v, strconv = %v, %v", text, got, want, err)
		}
	})
}

// EncodeBinary writes what the type's receive function reads, and refuses what
// only its input function could: writing the text of "42" where an int4's four
// bytes belong would be accepted by the server as a different number.
func TestEncodeBinary(t *testing.T) {
	cases := []struct {
		name string
		oid  uint32
		arg  any
		want []byte
		null bool
		err  error
	}{
		{"int8", Int8, int64(1), []byte{0, 0, 0, 0, 0, 0, 0, 1}, false, nil},
		{"int for int2", Int2, 7, []byte{0, 7}, false, nil},
		{"bool", Bool, true, []byte{1}, false, nil},
		{"text", Text, "tab\there", []byte("tab\there"), false, nil},
		{"int for text", Text, 42, []byte("42"), false, nil},
		{"bytea", Bytea, []byte{0, 0xff}, []byte{0, 0xff}, false, nil},
		{"json", JSON, `{"a":1}`, []byte(`{"a":1}`), false, nil},
		{"jsonb", JSONB, `{"a":1}`, append([]byte{1}, `{"a":1}`...), false, nil},
		{"jsonb value", JSONB, map[string]int{"a": 1}, append([]byte{1}, `{"a":1}`...), false, nil},
		{"null", Int4, nil, nil, true, nil},
		{"null jsonb", JSONB, nil, nil, true, nil},
		{"nil pointer", Text, (*string)(nil), nil, true, nil},
		{"string for int4", Int4, "42", nil, false, ErrNoBinary},
		{"string for timestamptz", Timestamptz, "tomorrow", nil, false, ErrNoBinary},
		{"hex for bytea", Bytea, `\x00ff`, nil, false, ErrNoBinary},
		{"time for time of day", Time, time.Unix(0, 0), nil, false, ErrNoBinary},
		{"overflow", Int2, 70000, nil, false, ErrOverflow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, null, err := EncodeBinary([]byte("kept"), c.oid, c.arg)
			if !errors.Is(err, c.err) || null != c.null {
				t.Fatalf("EncodeBinary = null %v, %v; want null %v, %v", null, err, c.null, c.err)
			}
			if string(got) != "kept"+string(c.want) {
				t.Fatalf("EncodeBinary appended %q, want %q", got[4:], c.want)
			}
		})
	}
}

func TestBinaryInput(t *testing.T) {
	for _, oid := range []uint32{Bool, Bytea, Int2, Int4, Int8, Float4, Float8, Text, Varchar, Bpchar, Date, Timestamp, Timestamptz, UUID, JSON, JSONB} {
		if !BinaryInput(oid) {
			t.Errorf("BinaryInput(%d) = false", oid)
		}
	}
	// numeric has a binary form the codec does not write; name and "char"
	// receive more strictly than they parse.
	for _, oid := range []uint32{Numeric, Name, Char, 1007, 600} {
		if BinaryInput(oid) {
			t.Errorf("BinaryInput(%d) = true", oid)
		}
	}
}

// A float64 too large for a real used to be narrowed to infinity and sent, so
// the server stored Infinity where its own parser would have refused the value.
func TestEncodeRefusesFloatOverflowingReal(t *testing.T) {
	if _, _, _, err := Encode(nil, Float4, 1e300); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Encode(1e300 as real) = %v, want ErrOverflow", err)
	}
	for _, f := range []float64{math.MaxFloat32, math.Inf(1), math.Inf(-1), math.NaN(), 1.5} {
		if _, format, _, err := Encode(nil, Float4, f); err != nil || format != 1 {
			t.Errorf("Encode(%v as real) = format %d, %v", f, format, err)
		}
	}
}
