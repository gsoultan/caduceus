// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import (
	"errors"
	"math/big"
	"reflect"
	"testing"
)

// Array literals: shapes, quoting, NULL, bounds; and what is not an array.
// A two-dimensional literal used to be refused, since its inner level ends
// before the outer one records its depth.
func TestParseTextArray(t *testing.T) {
	good := []struct {
		literal  string
		dims     []int
		elements []string
	}{
		{"{}", nil, nil},
		{"{1,2,3}", []int{3}, []string{"1", "2", "3"}},
		{`{"a b","c\"d",NULL,"NULL", e f }`, []int{5}, []string{"a b", `c"d`, "<nil>", "NULL", "e f"}},
		{"{{1,2},{3,4},{5,6}}", []int{3, 2}, []string{"1", "2", "3", "4", "5", "6"}},
		{"[0:1]={7,8}", []int{2}, []string{"7", "8"}},
		{`{a\,b}`, []int{1}, []string{"a,b"}},
	}
	for _, test := range good {
		array, err := parseTextArray([]byte(test.literal))
		if err != nil {
			t.Errorf("%s: %v", test.literal, err)
			continue
		}
		var elements []string
		for _, e := range array.elements {
			if e == nil {
				elements = append(elements, "<nil>")
				continue
			}
			elements = append(elements, string(e))
		}
		if !reflect.DeepEqual(array.dims, test.dims) || !reflect.DeepEqual(elements, test.elements) {
			t.Errorf("%s = %v %q", test.literal, array.dims, elements)
		}
	}
	for _, bad := range []string{"{1,2", "{{1,2},{3}}", "{1,{2}}", "{{1},2}", `{"a}`, "{1,,2}", "{1} x", "[1:2]{1}"} {
		if _, err := parseTextArray([]byte(bad)); !errors.Is(err, errTextArray) {
			t.Errorf("%s = %v, want errTextArray", bad, err)
		}
	}
}

// Range literals as range_out prints them.
func TestParseRange(t *testing.T) {
	r, err := parseRange(`["a ""b"" \\c",)`)
	if err != nil || *r.lower != `a "b" \c` || r.upper != nil || r.lowerType != Inclusive || r.upperType != Unbounded {
		t.Fatalf("quoted bound = %+v, %v", r, err)
	}
	for _, bad := range []string{"[1,2", "1,2)", "[1)", `["1,2)`, "[1,2,3)"} {
		if _, err := parseRange(bad); !errors.Is(err, errRange) {
			t.Errorf("%s = %v, want errRange", bad, err)
		}
	}
	if got := splitRanges(`[1,3), ["a,b",c]`); len(got) != 2 || got[1] != `["a,b",c]` {
		t.Errorf("splitRanges = %q", got)
	}
}

// hstore and record literals, with every kind of quoting.
func TestParseHstoreAndRecord(t *testing.T) {
	h, err := parseHstore(`"a"=>"1", "b c"=>NULL, "q\""=>"\\"`)
	if err != nil || *h["a"] != "1" || h["b c"] != nil || *h[`q"`] != `\` {
		t.Fatalf("hstore = %v, %v", h, err)
	}
	for _, bad := range []string{`"a"=>`, `"a"="1"`, `a=>"1"`, `"a"=>"1" "b"=>"2"`} {
		if _, err := parseHstore(bad); err == nil {
			t.Errorf("%s parsed", bad)
		}
	}
	fields, err := parseRecord(`(1,,"",  "x ""y"" \\z",abc)`)
	want := []any{"1", nil, "", `  x "y" \z`, "abc"}
	if err != nil || len(fields) != len(want) {
		t.Fatalf("record = %q, %v", fields, err)
	}
	for i, field := range fields {
		if (want[i] == nil) != (field == nil) || (field != nil && string(field) != want[i]) {
			t.Errorf("field %d = %q, want %v", i, field, want[i])
		}
	}
	if _, err := parseRecord(`(1,"x)`); !errors.Is(err, errRecord) {
		t.Errorf("an unterminated record = %v", err)
	}
}

// A fraction is written as the exact decimal it is, and one with no finite
// decimal refused.
func TestExactDecimal(t *testing.T) {
	for fraction, want := range map[string]string{"3/2": "1.5", "-1/8": "-0.125", "7/1": "7", "1/20": "0.05", "123/1000": "0.123"} {
		r, _ := new(big.Rat).SetString(fraction)
		got, err := exactDecimal(nil, r)
		if err != nil || string(got) != want {
			t.Errorf("%s = %s, %v, want %s", fraction, got, err, want)
		}
	}
	if _, err := exactDecimal(nil, big.NewRat(1, 6)); !errors.Is(err, ErrInexact) {
		t.Errorf("1/6 = %v, want ErrInexact", err)
	}
}
