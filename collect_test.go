// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// collectOptions are the two ways a connection runs a statement: kept on the
// server, and described afresh in front of its rows.
var collectOptions = []struct {
	name    string
	options Options
}{
	{"kept", Options{}},
	{"unnamed", Options{StatementCacheCapacity: -1}},
}

type collectAudit struct {
	CreatedAt time.Time
	CreatedBy string
}

type collectUser struct {
	ID       int64
	UserName string `db:"login"`
	Email    *string
	Score    *float64
	Ignored  string `db:"-"`
	collectAudit
}

const collectUsers = `SELECT * FROM (VALUES
	(1::int8, 'ann', 'ann@example.com', 1.5::float8, '2026-01-02 03:04:05+00'::timestamptz, 'admin'),
	(2::int8, 'bob', NULL, NULL, '2026-02-03 04:05:06+00'::timestamptz, 'admin'),
	(3::int8, 'cy', 'cy@example.com', NULL, '2026-03-04 05:06:07+00'::timestamptz, 'import')
) AS u(id, login, email, score, created_at, created_by) ORDER BY id`

// usable proves the connection is back at a boundary after a collection,
// whatever ended it: a result left unread would make this ErrConnBusy.
func usable(t *testing.T, conn *Conn) {
	t.Helper()
	var n int
	if err := conn.QueryRow(t.Context(), "SELECT 41 + 1").Scan(&n); err != nil || n != 42 {
		t.Fatalf("connection afterwards: %d, %v", n, err)
	}
}

// TestLiveCollectRowsByName proves a struct is filled by column name through
// a db tag, an untagged name matched ignoring case and underscores, and the
// fields of an embedded struct, with NULL into a pointer field as nil and a
// db:"-" field left alone.
func TestLiveCollectRowsByName(t *testing.T) {
	for _, tc := range collectOptions {
		t.Run(tc.name, func(t *testing.T) {
			conn := liveConn(t, tc.options)
			for range 2 {
				rows, err := conn.Query(t.Context(), collectUsers)
				if err != nil {
					t.Fatal(err)
				}
				users, err := CollectRows(rows, RowToStructByName[collectUser])
				if err != nil {
					t.Fatal(err)
				}
				checkUsers(t, users)
				if tag := rows.Tag(); tag != "SELECT 3" || tag.RowsAffected() != 3 {
					t.Fatalf("Tag() = %q", tag)
				}
			}
			usable(t, conn)
		})
	}
}

func checkUsers(t *testing.T, users []collectUser) {
	t.Helper()
	if len(users) != 3 {
		t.Fatalf("%d users", len(users))
	}
	ann, bob, cy := users[0], users[1], users[2]
	if ann.ID != 1 || ann.UserName != "ann" || ann.Email == nil || *ann.Email != "ann@example.com" || ann.Score == nil || *ann.Score != 1.5 {
		t.Fatalf("ann = %+v", ann)
	}
	if !ann.CreatedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) || ann.CreatedBy != "admin" || ann.Ignored != "" {
		t.Fatalf("ann's audit = %+v", ann.collectAudit)
	}
	if bob.Email != nil || bob.Score != nil || bob.UserName != "bob" {
		t.Fatalf("bob = %+v, NULL should leave pointers nil", bob)
	}
	if cy.Email == nil || *cy.Email != "cy@example.com" || cy.Score != nil || cy.CreatedBy != "import" {
		t.Fatalf("cy = %+v", cy)
	}
}

// TestLiveRowToStructVariants proves the remaining struct mappings against
// the server: by position, by address, lax, and the errors of a column or a
// field left unmatched, which must leave the connection usable.
func TestLiveRowToStructVariants(t *testing.T) {
	type positional struct {
		ID   int64
		Name string
		Skip int `db:"-"`
		Note *string
	}
	type lax struct {
		ID      int64
		Login   string
		Missing string
	}
	type extra struct {
		ID int64
	}
	for _, tc := range collectOptions {
		t.Run(tc.name, func(t *testing.T) {
			conn := liveConn(t, tc.options)
			ctx := t.Context()
			const two = "SELECT g::int8 AS id, 'n' || g AS login, NULLIF('x', 'x') AS note FROM generate_series(1, 2) g"

			rows, _ := conn.Query(ctx, two)
			byPos, err := CollectRows(rows, RowToStructByPos[positional])
			if err != nil || len(byPos) != 2 || byPos[1].ID != 2 || byPos[1].Name != "n2" || byPos[1].Note != nil {
				t.Fatalf("by position = %+v, %v", byPos, err)
			}

			rows, _ = conn.Query(ctx, two)
			addrs, err := CollectRows(rows, RowToAddrOfStructByPos[positional])
			if err != nil || len(addrs) != 2 || addrs[0] == addrs[1] || addrs[0].Name != "n1" {
				t.Fatalf("by position, by address = %+v, %v", addrs, err)
			}

			rows, _ = conn.Query(ctx, "SELECT 1::int8 AS id, 'ann' AS login")
			l, err := CollectOneRow(rows, RowToStructByNameLax[lax])
			if err != nil || l != (lax{ID: 1, Login: "ann"}) {
				t.Fatalf("lax = %+v, %v", l, err)
			}

			rows, _ = conn.Query(ctx, "SELECT 1::int8 AS id, 'ann' AS login")
			la, err := CollectOneRow(rows, RowToAddrOfStructByNameLax[lax])
			if err != nil || *la != (lax{ID: 1, Login: "ann"}) {
				t.Fatalf("lax, by address = %+v, %v", la, err)
			}

			rows, _ = conn.Query(ctx, "SELECT 1::int8 AS id")
			na, err := CollectOneRow(rows, RowToAddrOfStructByName[extra])
			if err != nil || na.ID != 1 {
				t.Fatalf("by name, by address = %+v, %v", na, err)
			}

			failures := []struct {
				name string
				run  func() error
				want string
			}{
				{"a field without a column", func() error {
					rows, _ := conn.Query(ctx, "SELECT g::int8 AS id, 'n' AS login FROM generate_series(1, 2) g")
					_, err := CollectRows(rows, RowToStructByName[lax])
					return err
				}, "field Missing"},
				{"a column without a field", func() error {
					rows, _ := conn.Query(ctx, two)
					_, err := CollectRows(rows, RowToStructByNameLax[extra])
					return err
				}, `column "login" has no field`},
				{"by position, a column too many", func() error {
					rows, _ := conn.Query(ctx, two)
					_, err := CollectRows(rows, RowToStructByPos[extra])
					return err
				}, "3 columns in the result, 1 fields"},
			}
			for _, f := range failures {
				if err := f.run(); err == nil || !strings.Contains(err.Error(), f.want) {
					t.Fatalf("%s: err = %v, want %q", f.name, err, f.want)
				}
				usable(t, conn)
			}
		})
	}
}

// TestLiveStructMappingAsPgx proves against the server the struct mappings
// that used to differ from pgx. A struct's own ID, beside an embedded struct
// with an ID of its own, used to lose the column to the embedded one: strict
// refused the struct with "field ID has no column", and lax left the ID the
// caller reads at zero. A tagged embedded struct used to be one field wanting
// a column of its tag's name, where pgx flattens it. And a db tag used to
// take a column of another case, which pgx refuses.
func TestLiveStructMappingAsPgx(t *testing.T) {
	type base struct {
		ID   int64
		Name string
	}
	type shadow struct {
		base
		ID int64
	}
	type pair struct{ A, B int64 }
	type taggedEmbed struct {
		pair `db:"pair"`
		C    int64
	}
	type tagged struct {
		Login string `db:"login"`
	}
	for _, tc := range collectOptions {
		t.Run(tc.name, func(t *testing.T) {
			conn := liveConn(t, tc.options)
			ctx := t.Context()
			const shadowed = "SELECT 7::int8 AS id, 'x' AS name"

			rows, _ := conn.Query(ctx, shadowed)
			strict, err := CollectOneRow(rows, RowToStructByName[shadow])
			if err != nil || strict.ID != 7 || strict.base.ID != 0 || strict.Name != "x" {
				t.Fatalf("shadowed, strict = %+v, %v; want the struct's own ID 7", strict, err)
			}

			rows, _ = conn.Query(ctx, shadowed)
			lax, err := CollectOneRow(rows, RowToStructByNameLax[shadow])
			if err != nil || lax.ID != 7 || lax.base.ID != 0 {
				t.Fatalf("shadowed, lax = %+v, %v; want the struct's own ID 7", lax, err)
			}

			const abc = "SELECT 1::int8 AS a, 2::int8 AS b, 3::int8 AS c"
			want := taggedEmbed{pair{1, 2}, 3}
			rows, _ = conn.Query(ctx, abc)
			byName, err := CollectOneRow(rows, RowToStructByName[taggedEmbed])
			if err != nil || byName != want {
				t.Fatalf("tagged embedded, by name = %+v, %v", byName, err)
			}
			rows, _ = conn.Query(ctx, abc)
			byPos, err := CollectOneRow(rows, RowToStructByPos[taggedEmbed])
			if err != nil || byPos != want {
				t.Fatalf("tagged embedded, by position = %+v, %v", byPos, err)
			}

			rows, _ = conn.Query(ctx, `SELECT 'ann' AS "Login"`)
			_, err = CollectOneRow(rows, RowToStructByName[tagged])
			if err == nil || !strings.Contains(err.Error(), `column "Login" has no field`) {
				t.Fatalf("a tag of another case: err = %v", err)
			}
			usable(t, conn)
		})
	}
}

// TestLiveRowToScalarsAndMaps proves RowTo, RowToAddrOf and RowToMap, and that
// the values Values and RowToMap return are the row's own, not views of the
// read buffer that the next row overwrites.
func TestLiveRowToScalarsAndMaps(t *testing.T) {
	for _, tc := range collectOptions {
		t.Run(tc.name, func(t *testing.T) {
			conn := liveConn(t, tc.options)
			ctx := t.Context()

			rows, _ := conn.Query(ctx, "SELECT g FROM generate_series(1, 4) g")
			ints, err := CollectRows(rows, RowTo[int32])
			if err != nil || !reflect.DeepEqual(ints, []int32{1, 2, 3, 4}) {
				t.Fatalf("RowTo = %v, %v", ints, err)
			}

			rows, _ = conn.Query(ctx, "SELECT NULLIF(g, 2)::text FROM generate_series(1, 3) g")
			ptrs, err := CollectRows(rows, RowToAddrOf[*string])
			if err != nil || len(ptrs) != 3 || *ptrs[0] == nil || **ptrs[0] != "1" || *ptrs[1] != nil {
				t.Fatalf("RowToAddrOf = %v, %v", ptrs, err)
			}

			rows, _ = conn.Query(ctx, "SELECT g::int8 AS id, decode(repeat(lpad(to_hex(g), 2, '0'), 2), 'hex') AS raw, NULL::text AS none, 'x' || g AS s FROM generate_series(10, 12) g")
			maps, err := CollectRows(rows, RowToMap)
			if err != nil || len(maps) != 3 {
				t.Fatalf("RowToMap = %v, %v", maps, err)
			}
			for i, m := range maps {
				id := int64(10 + i)
				raw := bytes.Repeat([]byte{byte(id)}, 2)
				if m["id"] != id || !bytes.Equal(m["raw"].([]byte), raw) || m["none"] != nil || len(m) != 4 {
					t.Fatalf("row %d = %v", i, m)
				}
			}
			if maps[0]["s"] != "x10" {
				t.Fatalf("an earlier row's text changed to %v", maps[0]["s"])
			}

			rows, _ = conn.Query(ctx, "SELECT 1::int2, 2::int4, 3::int8, 1.5::float8, true, 'a'::text, '\\x0102'::bytea, NULL::int4")
			values, err := CollectOneRow(rows, func(row CollectableRow) ([]any, error) { return row.Values() })
			want := []any{int16(1), int32(2), int64(3), 1.5, true, "a", []byte{1, 2}, nil}
			if err != nil || !reflect.DeepEqual(values, want) {
				t.Fatalf("Values = %#v, %v", values, err)
			}
			usable(t, conn)
		})
	}
}

// TestLiveValuesBeforeNext proves Values on no row is ErrNotOnARow, as Scan
// is, rather than an empty row, and that RawValues and Fields are what the
// CollectableRow a RowToFunc sees says they are.
func TestLiveValuesBeforeNext(t *testing.T) {
	conn := liveConn(t, Options{StatementCacheCapacity: -1})
	rows, err := conn.Query(t.Context(), "SELECT 1 AS one")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Fields()) != 1 {
		t.Fatal("no description")
	}
	if _, err := rows.Values(); !errors.Is(err, ErrNotOnARow) {
		t.Fatalf("Values before Next = %v", err)
	}
	got, err := CollectOneRow(rows, func(row CollectableRow) (string, error) {
		return row.Fields()[0].Name + "=" + string(row.RawValues()[0]), nil
	})
	if err != nil || got != "one=\x00\x00\x00\x01" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := rows.Values(); !errors.Is(err, ErrNotOnARow) {
		t.Fatalf("Values after the end = %v", err)
	}
}

// TestLiveCollectOneRow proves CollectOneRow takes the first row, reads the
// rest off the connection so it is free, and says ErrNoRows for none; and
// that CollectExactlyOneRow refuses a second row with ErrTooManyRows.
func TestLiveCollectOneRow(t *testing.T) {
	for _, tc := range collectOptions {
		t.Run(tc.name, func(t *testing.T) {
			conn := liveConn(t, tc.options)
			ctx := t.Context()
			tests := []struct {
				name    string
				sql     string
				collect func(*Rows, RowToFunc[int64]) (int64, error)
				want    int64
				err     error
			}{
				{"one of many", "SELECT g::int8 FROM generate_series(7, 1000) g", CollectOneRow[int64], 7, nil},
				{"one of none", "SELECT 1::int8 WHERE false", CollectOneRow[int64], 0, ErrNoRows},
				{"exactly one", "SELECT 5::int8", CollectExactlyOneRow[int64], 5, nil},
				{"exactly one of none", "SELECT 1::int8 WHERE false", CollectExactlyOneRow[int64], 0, ErrNoRows},
				{"exactly one of two", "SELECT g::int8 FROM generate_series(1, 2) g", CollectExactlyOneRow[int64], 0, ErrTooManyRows},
				{"exactly one of many", "SELECT g::int8 FROM generate_series(1, 1000) g", CollectExactlyOneRow[int64], 0, ErrTooManyRows},
			}
			for _, tt := range tests {
				rows, err := conn.Query(ctx, tt.sql)
				if err != nil {
					t.Fatal(err)
				}
				got, err := tt.collect(rows, RowTo[int64])
				if got != tt.want || !errors.Is(err, tt.err) {
					t.Fatalf("%s = %d, %v; want %d, %v", tt.name, got, err, tt.want, tt.err)
				}
				usable(t, conn)
			}
		})
	}
}

// TestLiveCollectErrors proves that what ends a collection early, the
// server's error after rows have arrived or a RowToFunc's own, is returned,
// with nothing collected, and the connection left usable; and that an error
// before any row, or after the one CollectOneRow takes, reaches it as that
// error, not ErrNoRows or a value.
func TestLiveCollectErrors(t *testing.T) {
	errMine := errors.New("mine")
	for _, tc := range collectOptions {
		t.Run(tc.name, func(t *testing.T) {
			conn := liveConn(t, tc.options)
			ctx := t.Context()
			const divides = "SELECT (10 / (3 - g))::int8 FROM generate_series(1, 5) g"

			rows, _ := conn.Query(ctx, divides)
			got, err := CollectRows(rows, RowTo[int64])
			if pgErr := (*wire.Error)(nil); got != nil || !errors.As(err, &pgErr) || pgErr.SQLState() != "22012" {
				t.Fatalf("server error mid-collection = %v, %v", got, err)
			}
			usable(t, conn)

			rows, _ = conn.Query(ctx, divides)
			kept, err := AppendRows([]int64{99}, rows, RowTo[int64])
			if kept != nil || err == nil {
				t.Fatalf("AppendRows = %v, %v", kept, err)
			}
			usable(t, conn)

			rows, _ = conn.Query(ctx, "SELECT g::int8 FROM generate_series(1, 1000) g")
			calls := 0
			_, err = CollectRows(rows, func(row CollectableRow) (int64, error) {
				if calls++; calls == 3 {
					return 0, errMine
				}
				return RowTo[int64](row)
			})
			if !errors.Is(err, errMine) || calls != 3 {
				t.Fatalf("RowToFunc's error = %v after %d calls", err, calls)
			}
			usable(t, conn)

			// The first row arrived whole; the error behind it is still the
			// result's, and CollectOneRow used to be able to drop it.
			rows, _ = conn.Query(ctx, divides)
			if one, err := CollectOneRow(rows, RowTo[int64]); one != 0 || err == nil {
				t.Fatalf("CollectOneRow with an error after its row = %d, %v", one, err)
			}
			usable(t, conn)

			rows, _ = conn.Query(ctx, "SELECT (1 / 0)::int8")
			if _, err := CollectOneRow(rows, RowTo[int64]); err == nil || errors.Is(err, ErrNoRows) {
				t.Fatalf("CollectOneRow of a failed query = %v", err)
			}
			usable(t, conn)

			rows, _ = conn.Query(ctx, "SELECT 'x'::text")
			if _, err := CollectOneRow(rows, RowTo[int64]); err == nil {
				t.Fatal("text scanned into int64")
			}
			usable(t, conn)
		})
	}
}

// TestLiveAppendRowsKeepsTheSlice proves AppendRows adds to what it is given.
func TestLiveAppendRowsKeepsTheSlice(t *testing.T) {
	conn := liveConn(t, Options{})
	type ids []int64
	rows, _ := conn.Query(t.Context(), "SELECT g::int8 FROM generate_series(1, 2) g")
	got, err := AppendRows(ids{0}, rows, RowTo[int64])
	if err != nil || !reflect.DeepEqual(got, ids{0, 1, 2}) {
		t.Fatalf("AppendRows = %v, %v", got, err)
	}
}

// TestLiveForEachRow proves ForEachRow scans each row before fn sees it,
// returns the command tag, stops at fn's error with the connection usable,
// and reports a scan that does not fit.
func TestLiveForEachRow(t *testing.T) {
	for _, tc := range collectOptions {
		t.Run(tc.name, func(t *testing.T) {
			conn := liveConn(t, tc.options)
			ctx := t.Context()
			var id int64
			var name string
			var seen []string

			rows, _ := conn.Query(ctx, "SELECT g::int8, 'n' || g FROM generate_series(1, 3) g")
			tag, err := ForEachRow(rows, []any{&id, &name}, func() error {
				seen = append(seen, name)
				return nil
			})
			if err != nil || tag != "SELECT 3" || tag.RowsAffected() != 3 || !reflect.DeepEqual(seen, []string{"n1", "n2", "n3"}) {
				t.Fatalf("ForEachRow = %q, %v, saw %v", tag, err, seen)
			}

			stop := errors.New("stop")
			rows, _ = conn.Query(ctx, "SELECT g::int8, 'n' || g FROM generate_series(1, 1000) g")
			tag, err = ForEachRow(rows, []any{&id, &name}, func() error {
				if id == 2 {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) || tag != "" {
				t.Fatalf("ForEachRow stopped = %q, %v", tag, err)
			}
			usable(t, conn)

			rows, _ = conn.Query(ctx, "SELECT 1, 2")
			if _, err := ForEachRow(rows, []any{&id}, nil); err == nil {
				t.Fatal("one destination for two columns accepted")
			}
			usable(t, conn)
		})
	}
}

// TestLiveCommandTags proves Exec, Tx.Exec, a batch's Exec and a pipeline's
// Exec all return a CommandTag that counts the rows touched.
func TestLiveCommandTags(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)

	tag, err := conn.Exec(ctx, "INSERT INTO "+table+" SELECT g, 'n' FROM generate_series(1, 5) g")
	if err != nil || !tag.Insert() || tag.RowsAffected() != 5 {
		t.Fatalf("Exec = %q, %v", tag, err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tag, err = tx.Exec(ctx, "UPDATE "+table+" SET note = $1 WHERE id <= $2", "m", 3)
	if err != nil || !tag.Update() || tag.RowsAffected() != 3 {
		t.Fatalf("Tx.Exec = %q, %v", tag, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	batch, err := conn.SendBatch(ctx, []Query{{SQL: "DELETE FROM " + table + " WHERE id = 1"}})
	if err != nil {
		t.Fatal(err)
	}
	tag, err = batch.Exec()
	if err != nil || !tag.Delete() || tag.RowsAffected() != 1 {
		t.Fatalf("BatchResults.Exec = %q, %v", tag, err)
	}
	if err := batch.Close(); err != nil {
		t.Fatal(err)
	}
	pipeline, err := conn.SendPipeline(ctx, []Query{{SQL: "SELECT * FROM " + table}})
	if err != nil {
		t.Fatal(err)
	}
	tag, err = pipeline.Exec()
	if err != nil || !tag.Select() || tag.RowsAffected() != 4 {
		t.Fatalf("PipelineResults.Exec = %q, %v", tag, err)
	}
	if err := pipeline.Close(); err != nil {
		t.Fatal(err)
	}
}
