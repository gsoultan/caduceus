// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"errors"
	"math/big"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/codec"
)

// Arrays of types with no binary form here arrive as array literals: an
// enum's, numeric's, inet's. They used to be refused, whatever the
// destination.
func TestLiveTextArrays(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	if _, err := conn.Exec(ctx, "DROP TYPE IF EXISTS caduceus_mood; CREATE TYPE caduceus_mood AS ENUM ('sad', 'ok', 'happy')"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(t.Context(), "DROP TYPE IF EXISTS caduceus_mood") })

	var moods []string
	if err := conn.QueryRow(ctx, "SELECT $1::caduceus_mood[]", []string{"ok", "happy"}).Scan(&moods); err != nil || !reflect.DeepEqual(moods, []string{"ok", "happy"}) {
		t.Errorf("enum[] = %v, %v", moods, err)
	}
	var numbers []float64
	if err := conn.QueryRow(ctx, "SELECT '{1.5,-2,3e2}'::numeric[]").Scan(&numbers); err != nil || !reflect.DeepEqual(numbers, []float64{1.5, -2, 300}) {
		t.Errorf("numeric[] = %v, %v", numbers, err)
	}
	var grid [][]string
	if err := conn.QueryRow(ctx, "SELECT '{{1.5,2},{3,4}}'::numeric[]").Scan(&grid); err != nil || !reflect.DeepEqual(grid, [][]string{{"1.5", "2"}, {"3", "4"}}) {
		t.Errorf("numeric[][] = %v, %v", grid, err)
	}
	var addresses []netip.Addr
	if err := conn.QueryRow(ctx, "SELECT '{10.0.0.1,::1}'::inet[]").Scan(&addresses); err != nil || len(addresses) != 2 || addresses[1] != netip.MustParseAddr("::1") {
		t.Errorf("inet[] = %v, %v", addresses, err)
	}
	var texts []*string
	if err := conn.QueryRow(ctx, `SELECT '{"a b",NULL,"say \"hi\"","back\\slash","","NULL"}'::text::text`).Scan(&texts); err != nil {
		t.Fatalf("a text array literal = %v", err)
	}
	want := []string{"a b", "", `say "hi"`, `back\slash`, "", "NULL"}
	for i, text := range texts {
		if (i == 1) != (text == nil) || (text != nil && *text != want[i]) {
			t.Errorf("element %d = %v", i, text)
		}
	}
}

// inet and cidr into *net.IPNet, macaddr into net.HardwareAddr, which used to
// get the bytes of its text, and *big.Rat sent as the decimal it is.
func TestLiveNetworkAndExactNumeric(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	var network net.IPNet
	if err := conn.QueryRow(ctx, "SELECT '10.1.2.3/8'::inet").Scan(&network); err != nil || network.String() != "10.1.2.3/8" || len(network.IP) != 4 {
		t.Errorf("inet into net.IPNet = %v, %v", network.String(), err)
	}
	if err := conn.QueryRow(ctx, "SELECT '::1'::inet").Scan(&network); err != nil || network.String() != "::1/128" {
		t.Errorf("an inet without a mask = %v, %v", network.String(), err)
	}
	var mac net.HardwareAddr
	if err := conn.QueryRow(ctx, "SELECT '08:00:2b:01:02:03'::macaddr").Scan(&mac); err != nil || mac.String() != "08:00:2b:01:02:03" {
		t.Errorf("macaddr = %v, %v", mac, err)
	}
	var text string
	_, cidr, _ := net.ParseCIDR("192.168.0.0/16")
	if err := conn.QueryRow(ctx, "SELECT $1::macaddr::text || ' ' || $2::cidr::text", mac, cidr).Scan(&text); err != nil || text != "08:00:2b:01:02:03 192.168.0.0/16" {
		t.Errorf("sent as macaddr and cidr = %q, %v", text, err)
	}
	if err := conn.QueryRow(ctx, "SELECT $1::numeric::text", big.NewRat(-1, 8)).Scan(&text); err != nil || text != "-0.125" {
		t.Errorf("big.Rat -1/8 = %q, %v", text, err)
	}
	if err := conn.QueryRow(ctx, "SELECT $1::numeric", big.NewRat(1, 3)).Scan(&text); !errors.Is(err, codec.ErrInexact) {
		t.Errorf("big.Rat 1/3 = %v, want ErrInexact", err)
	}
}

// Ranges and multiranges, in and out, with their bounds as their element
// types: canonical integers, times, dates, empty and unbounded.
func TestLiveRanges(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	var ints codec.Range[int32]
	if err := conn.QueryRow(ctx, "SELECT '[1,5]'::int4range").Scan(&ints); err != nil {
		t.Fatal(err)
	}
	if ints != (codec.Range[int32]{Lower: 1, Upper: 6, LowerType: codec.Inclusive, UpperType: codec.Exclusive, Valid: true}) {
		t.Errorf("int4range [1,5] = %+v, want [1,6)", ints)
	}
	var times codec.Range[time.Time]
	if err := conn.QueryRow(ctx, "SELECT tstzrange('2024-01-02 03:04:05+00', NULL, '(]')").Scan(&times); err != nil {
		t.Fatal(err)
	}
	if !times.Lower.Equal(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)) || times.LowerType != codec.Exclusive || times.UpperType != codec.Unbounded {
		t.Errorf("tstzrange = %+v", times)
	}
	var echoed codec.Range[time.Time]
	if err := conn.QueryRow(ctx, "SELECT $1::tstzrange", times).Scan(&echoed); err != nil || !echoed.Lower.Equal(times.Lower) || echoed.UpperType != codec.Unbounded {
		t.Errorf("tstzrange sent back = %+v, %v", echoed, err)
	}
	var dates codec.Range[time.Time]
	sent := codec.Range[time.Time]{Lower: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), Upper: time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC), LowerType: codec.Inclusive, UpperType: codec.Exclusive, Valid: true}
	if err := conn.QueryRow(ctx, "SELECT $1::daterange", sent).Scan(&dates); err != nil || !dates.Upper.Equal(sent.Upper) {
		t.Errorf("daterange = %+v, %v", dates, err)
	}
	var empty codec.Range[int64]
	if err := conn.QueryRow(ctx, "SELECT 'empty'::int8range").Scan(&empty); err != nil || empty.LowerType != codec.Empty || !empty.Valid {
		t.Errorf("empty = %+v, %v", empty, err)
	}
	var null codec.Range[int64]
	if err := conn.QueryRow(ctx, "SELECT NULL::int8range").Scan(&null); err != nil || null.Valid {
		t.Errorf("NULL = %+v, %v", null, err)
	}
	var text string
	if err := conn.QueryRow(ctx, "SELECT $1::int8range::text", codec.Range[int64]{LowerType: codec.Unbounded, Upper: 9, UpperType: codec.Inclusive, Valid: true}).Scan(&text); err != nil || text != "(,10)" {
		t.Errorf("(,9] sent = %q, %v", text, err)
	}

	var multi codec.Multirange[int64]
	if err := conn.QueryRow(ctx, "SELECT '{[1,3),[5,7)}'::int8multirange").Scan(&multi); err != nil || len(multi) != 2 || multi[1].Lower != 5 {
		t.Fatalf("int8multirange = %+v, %v", multi, err)
	}
	if err := conn.QueryRow(ctx, "SELECT $1::int8multirange::text", multi).Scan(&text); err != nil || text != "{[1,3),[5,7)}" {
		t.Errorf("int8multirange sent = %q, %v", text, err)
	}
	if err := conn.QueryRow(ctx, "SELECT $1::int8multirange::text", codec.Multirange[int64]{}).Scan(&text); err != nil || text != "{}" {
		t.Errorf("an empty multirange sent = %q, %v", text, err)
	}
}

// hstore, bit strings, the geometric types and records, in and out.
func TestLiveStructuredTypes(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	if _, err := conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS hstore"); err != nil {
		t.Skip("no hstore: ", err)
	}
	quoted, null := `say "hi" \ there`, (*string)(nil)
	store := codec.Hstore{"a": &quoted, "b": null, "=>": &quoted}
	var back codec.Hstore
	if err := conn.QueryRow(ctx, "SELECT $1::hstore", store).Scan(&back); err != nil || len(back) != 3 || *back["a"] != quoted || back["b"] != nil || *back["=>"] != quoted {
		t.Errorf("hstore = %v, %v", back, err)
	}

	var bits codec.Bits
	if err := conn.QueryRow(ctx, "SELECT B'1011001'::varbit").Scan(&bits); err != nil || bits.Len != 7 || bits.Bytes[0] != 0b10110010 {
		t.Errorf("varbit = %+v, %v", bits, err)
	}
	var text string
	if err := conn.QueryRow(ctx, "SELECT $1::varbit::text", bits).Scan(&text); err != nil || text != "1011001" {
		t.Errorf("varbit sent = %q, %v", text, err)
	}

	geometries := []struct {
		literal, cast string
		dest          any
		want          string
	}{
		{"(1.5,-2)", "point", &codec.Point{}, "(1.5,-2)"},
		{"((0,0),(2,3))", "box", &codec.Box{}, "(2,3),(0,0)"},
		{"[(0,0),(2,3)]", "lseg", &codec.Lseg{}, "[(0,0),(2,3)]"},
		{"{1,-1,0}", "line", &codec.Line{}, "{1,-1,0}"},
		{"<(1,2),3>", "circle", &codec.Circle{}, "<(1,2),3>"},
		{"[(0,0),(1,1),(2,0)]", "path", &codec.Path{}, "[(0,0),(1,1),(2,0)]"},
		{"((0,0),(1,1),(2,0))", "path", &codec.Path{}, "((0,0),(1,1),(2,0))"},
		{"((0,0),(1,1),(2,0))", "polygon", &codec.Polygon{}, "((0,0),(1,1),(2,0))"},
	}
	for _, g := range geometries {
		if err := conn.QueryRow(ctx, "SELECT $1::"+g.cast, g.literal).Scan(g.dest); err != nil {
			t.Errorf("%s %s: %v", g.cast, g.literal, err)
			continue
		}
		if err := conn.QueryRow(ctx, "SELECT $1::"+g.cast+"::text", reflect.ValueOf(g.dest).Elem().Interface()).Scan(&text); err != nil || text != g.want {
			t.Errorf("%s %s sent back = %q, %v", g.cast, g.literal, text, err)
		}
	}

	var id int
	var name string
	var missing *string
	var when time.Time
	if err := conn.QueryRow(ctx, `SELECT ROW(7, 'a "b", c', NULL, '2024-01-02 03:04:05+00'::timestamptz)`).Scan(codec.CompositeFields{&id, &name, &missing, &when}); err != nil {
		t.Fatal(err)
	}
	if id != 7 || name != `a "b", c` || missing != nil || !when.Equal(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("record = %d %q %v %v", id, name, missing, when)
	}
	if _, err := conn.Exec(ctx, "DROP TYPE IF EXISTS caduceus_pair; CREATE TYPE caduceus_pair AS (n int, s text, z text)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(t.Context(), "DROP TYPE IF EXISTS caduceus_pair") })
	var z *string
	if err := conn.QueryRow(ctx, "SELECT ($1::caduceus_pair).n, ($1::caduceus_pair).s, ($1::caduceus_pair).z", codec.CompositeFields{42, `q"uote\`, nil}).Scan(&id, &name, &z); err != nil || id != 42 || name != `q"uote\` || z != nil {
		t.Errorf("record sent = %d %q %v, %v", id, name, z, err)
	}
}
