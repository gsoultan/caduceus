// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/codec"
	"github.com/gsoultan/caduceus/wire"
)

// liveSchema creates a schema and returns its name.
func liveSchema(t *testing.T, conn *Conn) string {
	t.Helper()
	name := "caduceus_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	mustExec(t, conn, "CREATE SCHEMA "+name)
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+name+" CASCADE") })
	return name
}

// sqlState returns the SQLSTATE of a server error, or "" for anything else.
func sqlState(err error) string {
	var failure *wire.Error
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}

// A batch closed without its results read used to read its last window as
// replies alone, telling none of them apart, so a description the server said
// no longer held was never dropped. A batch of writes, which is how a batch is
// most often run, then failed on every attempt, until 64 other statements had
// evicted it or the connection was closed.
func TestLiveBatchClosedUnreadDropsWhatNoLongerHolds(t *testing.T) {
	cases := []struct {
		name, columns, sql, alter string
		args                      []any
		// code is the SQLSTATE of the first attempt, which still has the
		// statement as it was remembered.
		code string
	}{
		{
			name: "a parameter changed type", columns: "id int4, note text",
			sql:   "UPDATE TABLE SET note = $2 WHERE id = $1",
			alter: "ALTER TABLE {t} ALTER COLUMN id TYPE text",
			args:  []any{"1", "b"}, code: "42883",
		},
		{
			name: "a column was added", columns: "id int4, note text",
			sql:   "INSERT INTO TABLE (id, note) VALUES ($1, $2) RETURNING *",
			alter: "ALTER TABLE {t} ADD COLUMN at timestamptz DEFAULT now()",
			args:  []any{2, "b"}, code: "08P01",
		},
	}
	shapes := []struct {
		name string
		read func(results *BatchResults) error
	}{
		{"closed unread", func(results *BatchResults) error { return results.Close() }},
		{"first result read", func(results *BatchResults) error {
			_, err := results.Exec()
			return errors.Join(err, results.Close())
		}},
	}
	for _, c := range cases {
		for _, shape := range shapes {
			t.Run(c.name+", "+shape.name, func(t *testing.T) {
				conn := liveConn(t, Options{StatementCacheCapacity: -1})
				table, sql := staleTable(t, conn, c.columns, "1, 'a'", c.sql, c.args...)
				mustExec(t, conn, strings.ReplaceAll(c.alter, "{t}", table))
				for attempt := range 2 {
					queries := []Query{{SQL: sql, Arguments: c.args}, {SQL: sql, Arguments: c.args}}
					results, err := conn.SendBatch(t.Context(), queries)
					if err != nil {
						t.Fatal(err)
					}
					err = shape.read(results)
					switch {
					case attempt == 0 && sqlState(err) != c.code:
						t.Fatalf("the first attempt = %v, want SQLSTATE %s", err, c.code)
					case attempt == 1 && err != nil:
						t.Fatalf("the second attempt = %v: what the first said no longer held was not dropped", err)
					}
				}
			})
		}
	}
}

// A statement that is not kept on the server is described afresh in front of
// its rows. The columns read before the first Next used to be the ones
// remembered, and taking them first is how database/sql's Columns and pgx's
// FieldDescriptions are used: once the statement had come to mean something
// else, a caller filed each value under another column's name, with no error.
// Here the same text means another table with its columns in another order.
func TestLiveFieldsBeforeNextAreThisExecutions(t *testing.T) {
	for _, options := range notKept {
		conn := liveConn(t, options)
		ctx := t.Context()
		a, b := liveSchema(t, conn), liveSchema(t, conn)
		mustExec(t, conn, "CREATE TABLE "+a+".account (debit int8, credit int8)")
		mustExec(t, conn, "CREATE TABLE "+b+".account (credit int8, debit int8)")
		mustExec(t, conn, "INSERT INTO "+a+".account VALUES (100, 7)")
		mustExec(t, conn, "INSERT INTO "+b+".account VALUES (7, 100)")
		const sql = "SELECT * FROM account WHERE debit >= $1"

		for round, schema := range []string{a, b, b, a} {
			mustExec(t, conn, "SET search_path TO "+schema)
			rows, err := conn.Query(ctx, sql, 0)
			if err != nil {
				t.Fatal(err)
			}
			fields := rows.Fields()
			if len(rows.RawValues()) != 0 {
				t.Errorf("round %d: RawValues before Next = %v, want nothing", round, rows.RawValues())
			}
			var first, second int64
			if err := rows.Scan(&first, &second); !errors.Is(err, ErrNotOnARow) {
				t.Errorf("round %d: Scan before Next = %v, want ErrNotOnARow", round, err)
			}
			if !rows.Next() {
				t.Fatalf("round %d: no row: %v", round, rows.Err())
			}
			if err := rows.Scan(&first, &second); err != nil {
				t.Fatal(err)
			}
			byName := map[string]int64{fields[0].Name: first, fields[1].Name: second}
			if byName["debit"] != 100 || byName["credit"] != 7 {
				t.Errorf("round %d, remembering %d: filed by the columns read first, debit = %d and credit = %d, want 100 and 7",
					round, options.DescriptionCacheCapacity, byName["debit"], byName["credit"])
			}
			if rows.Next() {
				t.Errorf("round %d: a second row", round)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		}

		// In a batch the same: its statement's own description is read first.
		mustExec(t, conn, "SET search_path TO "+b)
		results, err := conn.SendBatch(ctx, []Query{{SQL: sql, Arguments: []any{0}}})
		if err != nil {
			t.Fatal(err)
		}
		rows, err := results.Query()
		if err != nil {
			t.Fatal(err)
		}
		if names := []string{rows.Fields()[0].Name, rows.Fields()[1].Name}; !slices.Equal(names, []string{"credit", "debit"}) {
			t.Errorf("in a batch the columns are %v, want credit and debit", names)
		}
		if err := results.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// What is remembered of a statement that is not kept on the server is held
// against where an error arrives. Before the statement's Bind is answered, the
// error is the server not taking the statement, or an argument, as encoded for
// the types remembered, and the next execution asks afresh. It used to be
// dropped for an error of class 42 alone: a type the server no longer has
// (XX000) and a value the old type will not take (22P02) failed again on every
// attempt.
func TestLiveErrorBeforeTheBindDropsWhatIsRemembered(t *testing.T) {
	ctx := t.Context()
	options := Options{StatementCacheCapacity: -1}

	t.Run("a parameter's type was dropped", func(t *testing.T) {
		conn := liveConn(t, options)
		table := copyTable(t, conn, "id int4")
		mood := table + "_mood"
		mustExec(t, conn, "CREATE TYPE "+mood+" AS ENUM ('happy', 'sad', 'meh')")
		t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP TYPE IF EXISTS "+mood+" CASCADE") })
		mustExec(t, conn, "ALTER TABLE "+table+" ADD COLUMN m "+mood)
		mustExec(t, conn, "INSERT INTO "+table+" VALUES (1, 'happy')")
		sql := "SELECT id FROM " + table + " WHERE m = $1"
		var id int
		if err := conn.QueryRow(ctx, sql, "happy").Scan(&id); err != nil {
			t.Fatal(err)
		}
		// Taking a label out of an enum: a new type in the old one's place.
		mustExec(t, conn, "ALTER TYPE "+mood+" RENAME TO "+mood+"_old")
		mustExec(t, conn, "CREATE TYPE "+mood+" AS ENUM ('happy', 'sad')")
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN m TYPE "+mood+" USING m::text::"+mood)
		mustExec(t, conn, "DROP TYPE "+mood+"_old")
		if err := conn.QueryRow(ctx, sql, "happy").Scan(&id); sqlState(err) != "XX000" {
			t.Fatalf("parsed with a type that is gone = %v, want SQLSTATE XX000", err)
		}
		if err := conn.QueryRow(ctx, sql, "happy").Scan(&id); err != nil || id != 1 {
			t.Fatalf("asked about afresh: id = %d, %v; want 1", id, err)
		}
	})

	t.Run("an argument the old type will not take", func(t *testing.T) {
		conn := liveConn(t, options)
		table := copyTable(t, conn, "id int4, q int4")
		sql := "INSERT INTO " + table + " (id, q) VALUES ($1, $2)"
		if _, err := conn.Exec(ctx, sql, 1, 1); err != nil {
			t.Fatal(err)
		}
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN q TYPE numeric")
		if _, err := conn.Exec(ctx, sql, 2, 2.5); sqlState(err) != "22P02" {
			t.Fatalf("2.5 for a parameter remembered as an integer = %v, want SQLSTATE 22P02", err)
		}
		if _, err := conn.Exec(ctx, sql, 2, 2.5); err != nil {
			t.Fatalf("asked about afresh: %v", err)
		}
		if got := countRows(t, conn, table); got != 2 {
			t.Fatalf("%d rows, want 2", got)
		}
	})

	t.Run("an expression of the old type", func(t *testing.T) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, flag bool", "1, true", "UPDATE TABLE SET flag = $1 WHERE id = 1", "1")
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN flag TYPE int4 USING flag::int4")
		if _, err := conn.Exec(ctx, sql, "1"); sqlState(err) != "42804" {
			t.Fatalf("a boolean for a column now an integer = %v, want SQLSTATE 42804", err)
		}
		if _, err := conn.Exec(ctx, sql, "1"); err != nil {
			t.Fatalf("asked about afresh: %v", err)
		}
	})
}

// An error raised as the statement runs is the statement's own, and says
// nothing of its types. One of class 42 used to drop what was remembered all
// the same, a row-level security policy refusing a row or a function raising
// an undefined table, and every execution after one paid a round trip to ask
// about the statement again.
func TestLiveErrorAsTheStatementRunsKeepsWhatIsRemembered(t *testing.T) {
	conn, trips := countedConn(t, Options{StatementCacheCapacity: -1})
	ctx := t.Context()
	name := copyTable(t, conn, "id int4")
	function := name + "_check"
	mustExec(t, conn, "CREATE FUNCTION "+function+"(x int4) RETURNS int4 LANGUAGE plpgsql AS $$ "+
		"BEGIN IF x < 0 THEN RAISE EXCEPTION 'negative' USING ERRCODE = '42P01'; END IF; RETURN x; END $$")
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP FUNCTION IF EXISTS "+function) })
	sql := "SELECT " + function + "($1)"
	var got int
	if err := conn.QueryRow(ctx, sql, 1).Scan(&got); err != nil {
		t.Fatal(err)
	}
	trips()
	if err := conn.QueryRow(ctx, sql, -1).Scan(&got); sqlState(err) != "42P01" {
		t.Fatalf("the function's own error = %v, want SQLSTATE 42P01", err)
	}
	trips()
	if err := conn.QueryRow(ctx, sql, 2).Scan(&got); err != nil || got != 2 {
		t.Fatalf("after the error: %d, %v; want 2", got, err)
	}
	if n := trips(); n != 1 {
		t.Errorf("after an error the statement raised as it ran it took %d round trips, want 1", n)
	}
}

// A statement not kept on the server runs, in the same Sync, before the
// server's description of it is read. A column that came to be a type the
// codec cannot read in the format asked for the old one is found only once the
// statement has run, and a write that returns it has been done: the error says
// so, ErrResultChanged, which tells "it ran and its result cannot be read"
// from "it did not run". Numeric, the commonest such change, is read in binary
// as the server prints it, where it used to be refused too.
func TestLiveResultThatCannotBeReadSaysTheStatementRan(t *testing.T) {
	ctx := t.Context()
	options := Options{StatementCacheCapacity: -1}

	t.Run("an integer altered to numeric", func(t *testing.T) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, balance int8", "1, 100",
			"UPDATE TABLE SET balance = balance + $1 WHERE id = 1 RETURNING balance", 10)
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN balance TYPE numeric(12,2)")
		var text string
		var amount float64
		if err := conn.QueryRow(ctx, sql, 10).Scan(&text); err != nil || text != "120.00" {
			t.Fatalf("a numeric in binary = %q, %v; want 120.00", text, err)
		}
		if err := conn.QueryRow(ctx, sql, 10).Scan(&amount); err != nil || amount != 130 {
			t.Fatalf("asked about afresh: %v, %v; want 130", amount, err)
		}
	})

	t.Run("an integer altered to a range", func(t *testing.T) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, hits int4", "1, 0",
			"UPDATE TABLE SET hits = hits + 1 WHERE hits >= $1 RETURNING id", 0)
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN id TYPE int4range USING int4range(id, id + 1)")
		var id string
		err := conn.QueryRow(ctx, sql, 0).Scan(&id)
		if !errors.Is(err, ErrResultChanged) {
			t.Fatalf("a range in binary = %q, %v; want ErrResultChanged", id, err)
		}
		var hits int
		if err := conn.QueryRow(ctx, "SELECT hits FROM "+table).Scan(&hits); err != nil || hits != 2 {
			t.Fatalf("hits = %d, %v; want 2: the write the error came from ran", hits, err)
		}
		if err := conn.QueryRow(ctx, sql, 0).Scan(&id); err != nil || id != "[1,2)" {
			t.Fatalf("asked about afresh: %q, %v; want [1,2)", id, err)
		}
	})

	t.Run("a column read as it always is", func(t *testing.T) {
		// A failure that has nothing to do with the format is not one.
		conn := liveConn(t, options)
		_, sql := staleTable(t, conn, "id int4, note text", "1, NULL", "SELECT note FROM TABLE WHERE id = $1", 1)
		var note string
		if err := conn.QueryRow(ctx, sql, 1).Scan(&note); err == nil || errors.Is(err, ErrResultChanged) {
			t.Fatalf("a NULL into a string = %v, want an error that is not ErrResultChanged", err)
		}
	})
}

// Where a remembered statement comes to be described otherwise, the result is
// read as described now. Each case holds a part of that which nothing else
// does: the format of a column that is not the first, a statement that stops
// returning columns, a run of one statement in a batch of several, and windows
// of a pipeline encoded after the description changed.
func TestLiveRestatedResults(t *testing.T) {
	const big = int64(5_000_000_000)
	ctx := t.Context()
	options := Options{StatementCacheCapacity: -1}

	t.Run("formats per column, the second changed", func(t *testing.T) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, note text", "1, 'a'", "SELECT note, id FROM TABLE WHERE note = $1", "a")
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN id TYPE int8")
		mustExec(t, conn, "UPDATE "+table+" SET id = "+strconv.FormatInt(big, 10))
		var note string
		var id int64
		if err := conn.QueryRow(ctx, sql, "a").Scan(&note, &id); err != nil || note != "a" || id != big {
			t.Fatalf("%q, %d, %v; want a, %d", note, id, err, big)
		}
	})

	t.Run("a statement stops returning columns", func(t *testing.T) {
		conn := liveConn(t, options)
		name := copyTable(t, conn, "id int4")
		procedure := name + "_p"
		mustExec(t, conn, "CREATE PROCEDURE "+procedure+"(x int4, INOUT y int4) LANGUAGE plpgsql AS $$ BEGIN y := x; END $$")
		t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP PROCEDURE IF EXISTS "+procedure+"(int4)") })
		sql := "CALL " + procedure + "($1, NULL)"
		var y int
		if err := conn.QueryRow(ctx, sql, 3).Scan(&y); err != nil || y != 3 {
			t.Fatalf("%d, %v; want 3", y, err)
		}
		mustExec(t, conn, "DROP PROCEDURE "+procedure+"(int4, int4)")
		mustExec(t, conn, "CREATE PROCEDURE "+procedure+"(x int4, y int4) LANGUAGE plpgsql AS $$ BEGIN END $$")
		rows, err := conn.Query(ctx, sql, 3)
		if err != nil {
			t.Fatal(err)
		}
		if fields := rows.Fields(); len(fields) != 0 {
			t.Errorf("a CALL with no result has %d columns", len(fields))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if conn.cache.get(sql) != nil {
			t.Error("a statement that stopped returning columns was still remembered with them")
		}
	})

	// A run of one statement in a batch, or in a window of a pipeline inside
	// a transaction, shares one Parse and one description: those after the
	// first are read with what the first was told.
	several := func(t *testing.T, send func(conn *Conn, queries []Query) (interface {
		QueryRow() *Row
		Close() error
	}, error)) {
		conn := liveConn(t, options)
		table, stale := staleTable(t, conn, "id int4, note text", "1, 'a'", "SELECT id FROM TABLE WHERE note = $1", "a")
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN id TYPE int8")
		mustExec(t, conn, "UPDATE "+table+" SET id = "+strconv.FormatInt(big, 10))
		other := "SELECT $1::int4 + 7"
		queries := []Query{
			{SQL: stale, Arguments: []any{"a"}},
			{SQL: stale, Arguments: []any{"a"}},
			{SQL: other, Arguments: []any{1}},
			{SQL: stale, Arguments: []any{"a"}},
		}
		results, err := send(conn, queries)
		if err != nil {
			t.Fatal(err)
		}
		for i, query := range queries {
			var got int64
			want := big
			if query.SQL == other {
				want = 8
			}
			if err := results.QueryRow().Scan(&got); err != nil || got != want {
				t.Errorf("statement %d: %d, %v; want %d", i, got, err, want)
			}
		}
		if err := results.Close(); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("a batch of several statements", func(t *testing.T) {
		several(t, func(conn *Conn, queries []Query) (interface {
			QueryRow() *Row
			Close() error
		}, error) {
			return conn.SendBatch(ctx, queries)
		})
	})
	t.Run("a pipeline of several statements in a transaction", func(t *testing.T) {
		several(t, func(conn *Conn, queries []Query) (interface {
			QueryRow() *Row
			Close() error
		}, error) {
			tx, err := conn.Begin(ctx)
			if err != nil {
				return nil, err
			}
			t.Cleanup(func() { _ = tx.Rollback(ctx) })
			return tx.SendPipeline(ctx, queries)
		})
	})

	t.Run("windows encoded after the change", func(t *testing.T) {
		// Each window after the first is encoded with what the first was
		// told: its parameters and the formats asked for.
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, note text", "1, 'a'", "SELECT id FROM TABLE WHERE note = $1", "a")
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN id TYPE int8")
		mustExec(t, conn, "UPDATE "+table+" SET id = "+strconv.FormatInt(big, 10))
		queries := make([]Query, 3000)
		for i := range queries {
			queries[i] = Query{SQL: sql, Arguments: []any{"a"}}
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		results, err := tx.SendPipeline(ctx, queries)
		if err != nil {
			t.Fatal(err)
		}
		for i := range queries {
			var id int64
			if err := results.QueryRow().Scan(&id); err != nil || id != big {
				t.Fatalf("statement %d: %d, %v; want %d", i, id, err, big)
			}
		}
		if err := results.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a wrong count of arguments keeps it", func(t *testing.T) {
		// Refused before anything is sent, and nothing about the statement
		// has changed.
		conn, trips := countedConn(t, options)
		table := liveTable(t, conn)
		sql := "SELECT note FROM " + table + " WHERE id = $1"
		rows, err := conn.Query(ctx, sql, 1)
		if err != nil {
			t.Fatal(err)
		}
		rows.Close()
		var note string
		var miscounted *ArgumentCountError
		if err := conn.QueryRow(ctx, sql, 1, 2).Scan(&note); !errors.As(err, &miscounted) {
			t.Fatalf("two arguments for one parameter = %v, want an ArgumentCountError", err)
		}
		trips()
		if err := conn.QueryRow(ctx, sql, 1).Scan(&note); !errors.Is(err, ErrNoRows) {
			t.Fatal(err)
		}
		if n := trips(); n != 1 {
			t.Errorf("after a wrong count of arguments the statement took %d round trips, want 1", n)
		}
	})
}

// Inside a transaction a window of a pipeline shares one Sync, and on a
// connection that keeps no statements on the server a run of the same
// statement shares one Parse in it. Every Bind must still run the statement it
// was encoded for. These statements each give an answer no other gives, so
// binding the wrong Parse shows, where the inserts of the other tests would
// not: a Parse sent only for the first statement of a window, or taken as sent
// for a statement that could not be encoded, ran one statement with another's
// arguments and passed them all.
func TestLiveTransactionPipelineBindsEachStatementItsOwn(t *testing.T) {
	const a, b = "SELECT $1::int4 + 1000", "SELECT $1::int4 + 2000"
	unencodable := make(chan int)
	sequences := [][]Query{
		{{SQL: a, Arguments: []any{1}}, {SQL: a, Arguments: []any{2}}, {SQL: b, Arguments: []any{3}},
			{SQL: b, Arguments: []any{4}}, {SQL: a, Arguments: []any{5}}},
		{{SQL: a, Arguments: []any{1}}, {SQL: b, Arguments: []any{unencodable}}, {SQL: b, Arguments: []any{3}}},
	}
	want := func(query Query) int {
		offset := 1000
		if query.SQL == b {
			offset = 2000
		}
		return query.Arguments[0].(int) + offset
	}
	for _, options := range append([]Options{{}}, notKept...) {
		conn := liveConn(t, options)
		ctx := t.Context()
		for _, queries := range sequences {
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			results, err := tx.SendPipeline(ctx, queries)
			if err != nil {
				t.Fatal(err)
			}
			for i, query := range queries {
				var got int
				err := results.QueryRow().Scan(&got)
				var refused *ArgumentError
				switch {
				case query.Arguments[0] == any(unencodable):
					if !errors.As(err, &refused) {
						t.Errorf("statement %d: %v, want an ArgumentError", i, err)
					}
				case err != nil || got != want(query):
					t.Errorf("cache %d, remembering %d: statement %d (%s with %v) = %d, %v; want %d",
						options.StatementCacheCapacity, options.DescriptionCacheCapacity, i, query.SQL, query.Arguments[0], got, err, want(query))
				}
			}
			_ = results.Close()
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// inBinary runs sql with its first column asked for in binary whatever its
// type, which is what a statement does for a column described as a type read
// in binary that has changed since, and returns the result to read.
func inBinary(t *testing.T, conn *Conn, sql string, args ...any) *Rows {
	t.Helper()
	if err := conn.begin(t.Context()); err != nil {
		t.Fatal(err)
	}
	stmt, err := conn.prepare(sql)
	if err != nil {
		t.Fatal(conn.finish(err))
	}
	forced := *stmt
	forced.prev, forced.next = nil, nil
	forced.fields = slices.Clone(stmt.fields)
	forced.formats = make([]int16, len(forced.fields))
	for i := range forced.fields {
		forced.formats[i] = forced.fields[i].Format
	}
	forced.fields[0].Format, forced.formats[0] = wire.BinaryFormat, wire.BinaryFormat
	if err := conn.bind(&forced, args); err != nil {
		t.Fatal(conn.finish(err))
	}
	rows := new(Rows)
	rows.init(conn, &forced, false)
	return rows
}

// The types asked for as text arrive in binary when their statement asked for
// binary for a column that was another type: each is read as the server prints
// it. The server's own text of each value is what they are held to. An
// interval read as a string used to differ from it wherever a part was -1 or
// followed a negative one, and the elements of a bytea array were their bytes.
func TestLiveBinaryFormsReadAsTheServerPrintsThem(t *testing.T) {
	conn := liveConn(t, Options{})
	cases := []struct {
		cast   string
		values []string
	}{
		{"numeric", []string{"0", "1.5", "-123.4500", "1e20", "0.0001", "123456789.987654321", "NaN", "Infinity",
			"-Infinity", "-0.00012", "10000", "9999.9999", "1e-20", "-1", "100000000", "0.5000000000000000000001",
			"12345678901234567890.123456789"}},
		{"interval", []string{"-1 years -2 mons +3 days -04:05:06.789", "1 year 2 mons", "-1 days +02:00:00", "0",
			"1 day", "-1 day", "3 days 04:05:06", "-1 mons 3 days", "100 hours", "-0.5 seconds", "1 year -1 mon",
			"2 mons -3 days +04:00:00", "-1 year", "1 mon", "-178000000 years", "00:00:00.000001"}},
		{"bytea[]", []string{`{"\\x0102ff",NULL,"\\x"}`, `{{"\\x00"},{"\\x41"}}`, `{}`}},
	}
	for _, c := range cases {
		for _, value := range c.values {
			rows := inBinary(t, conn, "SELECT $1::text::"+c.cast+", ($1::text::"+c.cast+")::text", value)
			if !rows.Next() {
				t.Fatalf("%s %s: no row: %v", c.cast, value, rows.Err())
			}
			if rows.Fields()[0].Format != wire.BinaryFormat {
				t.Fatalf("%s %s arrived as text", c.cast, value)
			}
			var read, printed string
			if err := rows.Scan(&read, &printed); err != nil {
				t.Fatalf("%s %s: %v", c.cast, value, err)
			}
			rows.Close()
			if read != printed {
				t.Errorf("%s %s: read as %q, the server prints %q", c.cast, value, read, printed)
			}
		}
	}

	// A numeric read in binary into the destinations its text is read into.
	rows := inBinary(t, conn, "SELECT 1234.5::numeric, 0")
	defer rows.Close()
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	values := rows.RawValues()
	var f float64
	if err := codec.Scan(codec.Numeric, wire.BinaryFormat, values[0], &f); err != nil || f != 1234.5 {
		t.Errorf("into a float64: %v, %v; want 1234.5", f, err)
	}
	var n int64
	if err := codec.Scan(codec.Numeric, wire.BinaryFormat, values[0], &n); !errors.Is(err, codec.ErrOverflow) {
		t.Errorf("1234.5 into an int64 = %d, %v; want ErrOverflow", n, err)
	}
}
