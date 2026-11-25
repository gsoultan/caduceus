// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/caduceus/codec"
	"github.com/gsoultan/caduceus/wire"
)

// notKept are the ways a connection keeps no statements on the server, which
// is what a pooler in transaction mode calls for: remembering what the server
// said of each, and remembering nothing.
var notKept = []Options{
	{StatementCacheCapacity: -1},
	{StatementCacheCapacity: -1, DescriptionCacheCapacity: -1},
}

// carried returns the first way in which what a client sent relies on
// something an earlier Sync left on the server, or "" if every Sync stands
// alone. That is a statement with a name, which is named in order to outlive
// its Sync, or a Bind of the unnamed statement with no Parse ahead of it in
// its own Sync.
func carried(messages []sentMessage) string {
	parsed := false
	for _, message := range messages {
		switch message.kind {
		case 'P':
			if message.statement != "" {
				return "a Parse of a statement named " + message.statement
			}
			parsed = true
		case 'B':
			if message.statement != "" {
				return "a Bind of a statement named " + message.statement
			}
			if !parsed {
				return "a Bind of the unnamed statement, which nothing in its Sync had parsed"
			}
		case 'D', 'C':
			if message.statement != "" {
				return "a " + string(message.kind) + " of a statement named " + message.statement
			}
		case 'S', 'Q':
			parsed = false
		}
	}
	return ""
}

// everyKind runs every kind of exchange that binds a statement, each inserting
// rows with ids from next, and returns how many rows that was.
func everyKind(t *testing.T, conn *Conn, table string, next func() int, after func(name string)) int {
	t.Helper()
	ctx := t.Context()
	insert := "INSERT INTO " + table + " VALUES ($1, $2)"
	returning := insert + " RETURNING id"
	other := "INSERT INTO " + table + " (note, id) VALUES ($2, $1)"
	query := func(sql string) Query { return Query{SQL: sql, Arguments: []any{next(), "a"}} }
	closed := func(results interface{ Close() error }, err error) error {
		if err != nil {
			return err
		}
		return results.Close()
	}
	inserted := 0
	kinds := []struct {
		name string
		rows int
		run  func() error
	}{
		{"Exec", 1, func() error {
			_, err := conn.Exec(ctx, insert, next(), "a")
			return err
		}},
		{"Query", 1, func() error {
			rows, err := conn.Query(ctx, returning, next(), "a")
			if err != nil {
				return err
			}
			for rows.Next() {
			}
			return rows.Err()
		}},
		{"QueryRow", 1, func() error {
			var id int
			return conn.QueryRow(ctx, returning, next(), "a").Scan(&id)
		}},
		{"SendBatch of one statement", 3, func() error {
			return closed(conn.SendBatch(ctx, []Query{query(insert), query(insert), query(insert)}))
		}},
		{"SendBatch of several statements", 4, func() error {
			return closed(conn.SendBatch(ctx, []Query{query(insert), query(other), query(other), query(insert)}))
		}},
		{"SendBatch larger than a window", 300, func() error {
			queries := make([]Query, 300)
			for i := range queries {
				queries[i] = Query{SQL: insert, Arguments: []any{next(), strings.Repeat("x", 300)}}
				if i%100 == 99 {
					queries[i].SQL = other
				}
			}
			return closed(conn.SendBatch(ctx, queries))
		}},
		{"SendPipeline", 4, func() error {
			return closed(conn.SendPipeline(ctx, []Query{query(insert), query(insert), query(other), query(insert)}))
		}},
		{"SendPipeline in a transaction", 4, func() error {
			tx, err := conn.Begin(ctx)
			if err != nil {
				return err
			}
			err = closed(tx.SendPipeline(ctx, []Query{query(insert), query(insert), query(other), query(insert)}))
			return errors.Join(err, tx.Commit(ctx))
		}},
		{"a transaction", 2, func() error {
			tx, err := conn.Begin(ctx)
			if err != nil {
				return err
			}
			var id int
			_, err = tx.Exec(ctx, insert, next(), "a")
			err = errors.Join(err, tx.QueryRow(ctx, returning, next(), "a").Scan(&id))
			return errors.Join(err, tx.Commit(ctx))
		}},
		{"CopyFrom", 2, func() error {
			rows := [][]any{{next(), "a"}, {next(), "b"}}
			_, err := conn.CopyFrom(ctx, []string{table[1 : len(table)-1]}, []string{"id", "note"}, CopyFromRows(rows))
			return err
		}},
	}
	for _, kind := range kinds {
		// Twice: the first has the statements described, and the second finds
		// them remembered, on a connection that remembers.
		for _, attempt := range []string{"new", "known"} {
			if err := kind.run(); err != nil {
				t.Fatalf("%s (%s) = %v", kind.name, attempt, err)
			}
			inserted += kind.rows
			after(kind.name + " (" + attempt + ")")
		}
	}
	return inserted
}

// A connection that keeps no statements on the server is for a pooler in
// transaction mode, which gives every Sync whichever server connection is
// free. So nothing a Sync runs may have been left there by one before it.
//
// What used to go wrong: a statement was parsed and described in one Sync and
// bound in the next, with the unnamed statement trusted to last between them.
// Behind PgBouncer and behind gpoolproxy the Bind arrived where another client
// had parsed something else last, and ran that with this client's arguments:
// of 4,800 statements from 12 clients, each running its own, 4,393 came back
// with another client's answer. A batch and a pipeline gave their statements
// names for as long as they ran, which a pooler that does not follow names
// loses the same way, or finds on another client's statement of that name.
func TestLiveNothingOutlivesASyncWithoutStatementsKept(t *testing.T) {
	for _, options := range notKept {
		conn, sent := watchedConn(t, options)
		table := liveTable(t, conn)
		id := 0
		sent()
		inserted := everyKind(t, conn, table, func() int { id++; return id }, func(name string) {
			messages := sent()
			if len(messages) == 0 {
				t.Fatalf("%s: the relay saw nothing sent", name)
			}
			if how := carried(messages); how != "" {
				t.Errorf("%s, remembering %d: sent %s", name, options.DescriptionCacheCapacity, how)
			}
		})
		if got := countRows(t, conn, table); got != inserted {
			t.Errorf("remembering %d: %d rows, want %d", options.DescriptionCacheCapacity, got, inserted)
		}
	}

	// The check is not one that anything would pass: a connection that keeps
	// its statements on the server binds, in a Sync of its own, what an
	// earlier Sync parsed.
	conn, sent := watchedConn(t, Options{})
	table := liveTable(t, conn)
	sent()
	for id := range 2 {
		if _, err := conn.Exec(t.Context(), "INSERT INTO "+table+" VALUES ($1, $2)", id, "a"); err != nil {
			t.Fatal(err)
		}
	}
	if carried(sent()) == "" {
		t.Error("a statement kept on the server was not seen to be carried from one Sync to the next")
	}
}

// Once a connection that keeps no statements on the server has been told what
// a statement takes and returns, it runs it in one round trip, its Parse
// included. It used to ask every time, which made two of every statement. One
// that remembers nothing still asks every time.
func TestLiveKnownStatementIsOneRoundTripWithoutStatementsKept(t *testing.T) {
	for _, options := range notKept {
		conn, trips := countedConn(t, options)
		table := liveTable(t, conn)
		ctx := t.Context()
		insert := "INSERT INTO " + table + " VALUES ($1, $2)"
		id := 0
		queries := func() []Query {
			id += 3
			return []Query{
				{SQL: insert, Arguments: []any{id, "a"}},
				{SQL: insert + " RETURNING id", Arguments: []any{id + 1, "a"}},
				{SQL: insert, Arguments: []any{id + 2, "a"}},
			}
		}
		known := 1
		if options.DescriptionCacheCapacity < 0 {
			known = 2
		}
		// first is what the first run of each takes: two round trips where its
		// statement is new to the connection, and what a known one takes where
		// the kinds before it have run its statements already.
		kinds := []struct {
			name  string
			first int
			run   func() error
		}{
			{"Exec", 2, func() error {
				_, err := conn.Exec(ctx, insert, queries()[0].Arguments...)
				return err
			}},
			{"QueryRow", 2, func() error {
				var got int
				return conn.QueryRow(ctx, insert+" RETURNING id", queries()[0].Arguments...).Scan(&got)
			}},
			{"SendBatch", known, func() error {
				results, err := conn.SendBatch(ctx, queries())
				if err != nil {
					return err
				}
				return results.Close()
			}},
			{"SendPipeline", known, func() error {
				results, err := conn.SendPipeline(ctx, queries())
				if err != nil {
					return err
				}
				return results.Close()
			}},
		}
		for _, kind := range kinds {
			for attempt, want := range []int{kind.first, known, known} {
				trips()
				if err := kind.run(); err != nil {
					t.Fatalf("%s = %v", kind.name, err)
				}
				if got := trips(); got != want {
					t.Errorf("%s, attempt %d, remembering %d: %d round trips, want %d",
						kind.name, attempt, options.DescriptionCacheCapacity, got, want)
				}
			}
		}
	}
}

// staleTable is a table with one row, and a statement over it that the
// connection has run once and so remembers.
func staleTable(t *testing.T, conn *Conn, columns, values, sql string, args ...any) (string, string) {
	t.Helper()
	table := copyTable(t, conn, columns)
	if _, err := conn.Exec(t.Context(), "INSERT INTO "+table+" VALUES ("+values+")"); err != nil {
		t.Fatal(err)
	}
	sql = strings.ReplaceAll(sql, "TABLE", table)
	rows, err := conn.Query(t.Context(), sql, args...)
	if err != nil {
		t.Fatalf("%s = %v", sql, err)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("%s = %v", sql, err)
	}
	return table, sql
}

func mustExec(t *testing.T, conn *Conn, sql string) {
	t.Helper()
	if _, err := conn.Exec(t.Context(), sql); err != nil {
		t.Fatalf("%s = %v", sql, err)
	}
}

// What a connection remembers of a statement it does not keep on the server
// is held to what the server says of the statement each time it runs. A
// prepared statement has the server for that: a plan whose result changed
// type is refused there. A statement parsed afresh every time is never
// refused, so without the check a result would be read as the types it had
// when it was first asked about: eight bytes of a bigint as an integer, or a
// row with a column more than there are places for.
func TestLiveRememberedDescriptionFollowsTheServer(t *testing.T) {
	const big = int64(5_000_000_000)
	options := Options{StatementCacheCapacity: -1}
	ctx := t.Context()

	t.Run("a column changes type", func(t *testing.T) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, note text", "1, 'a'", "SELECT id FROM TABLE WHERE note = $1", "a")
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN id TYPE int8")
		mustExec(t, conn, "UPDATE "+table+" SET id = "+strconv.FormatInt(big, 10))
		// The first is read as the server describes it now, and the second
		// with the statement asked about afresh.
		for attempt := range 2 {
			var id int64
			if err := conn.QueryRow(ctx, sql, "a").Scan(&id); err != nil || id != big {
				t.Fatalf("attempt %d: id = %d, %v; want %d", attempt, id, err, big)
			}
		}
		rows, err := conn.Query(ctx, sql, "a")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if got := rows.Fields()[0].DataTypeOID; got != codec.Int8 {
			t.Errorf("the column is remembered as type %d, want int8", got)
		}
	})

	t.Run("a column changes to a type that is not read in binary", func(t *testing.T) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, note text", "1, 'a'", "SELECT id FROM TABLE WHERE note = $1", "a")
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN id TYPE numeric")
		mustExec(t, conn, "UPDATE "+table+" SET id = 1.5")
		// It was asked for in binary, as an integer is, and a numeric in
		// binary is read as the server prints it. It used to be refused, and a
		// write that returned it had been done all the same.
		for attempt := range 2 {
			var id string
			if err := conn.QueryRow(ctx, sql, "a").Scan(&id); err != nil || id != "1.5" {
				t.Fatalf("attempt %d: id = %q, %v; want 1.5", attempt, id, err)
			}
		}
	})

	t.Run("a parameter changes type", func(t *testing.T) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, note text", "1, 'a'", "SELECT note FROM TABLE WHERE id = $1", 1)
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN id TYPE int8")
		mustExec(t, conn, "UPDATE "+table+" SET id = "+strconv.FormatInt(big, 10))
		// The statement is parsed with the type it was described with, as a
		// prepared statement keeps its own, so a value only the new type holds
		// is refused, here, before anything is sent. That drops what was
		// remembered, and the next attempt learns the new type.
		var note string
		var refused *ArgumentError
		if err := conn.QueryRow(ctx, sql, big).Scan(&note); !errors.As(err, &refused) {
			t.Fatalf("a bigint for a parameter remembered as an integer = %v, want an ArgumentError", err)
		}
		if err := conn.QueryRow(ctx, sql, big).Scan(&note); err != nil || note != "a" {
			t.Fatalf("asked about afresh: note = %q, %v; want a", note, err)
		}
	})

	t.Run("a parameter changes to a type the server will not compare", func(t *testing.T) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, note text", "1, 'a'", "SELECT note FROM TABLE WHERE id = $1", 1)
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN id TYPE text")
		// Parsed with the integer it was described with, the comparison is
		// one the server has no operator for. An error of that class drops
		// what was remembered.
		var note string
		var refused *wire.Error
		if err := conn.QueryRow(ctx, sql, "1").Scan(&note); !errors.As(err, &refused) || refused.Code != "42883" {
			t.Fatalf("text compared with a parameter remembered as an integer = %v, want SQLSTATE 42883", err)
		}
		if err := conn.QueryRow(ctx, sql, "1").Scan(&note); err != nil || note != "a" {
			t.Fatalf("asked about afresh: note = %q, %v; want a", note, err)
		}
	})

	t.Run("a column is added, formats per column", func(t *testing.T) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, note text", "1, 'a'", "SELECT * FROM TABLE WHERE id = $1", 1)
		mustExec(t, conn, "ALTER TABLE "+table+" ADD COLUMN extra int4 DEFAULT 7")
		// Two formats for three columns is a Bind the server refuses, with
		// nothing run. It has described the three by then, which is what drops
		// what was remembered.
		var id, extra int
		var note string
		var refused *wire.Error
		if err := conn.QueryRow(ctx, sql, 1).Scan(&id, &note, &extra); !errors.As(err, &refused) || refused.Code != "08P01" {
			t.Fatalf("a Bind with a format too few = %v, want SQLSTATE 08P01", err)
		}
		if err := conn.QueryRow(ctx, sql, 1).Scan(&id, &note, &extra); err != nil || id != 1 || note != "a" || extra != 7 {
			t.Fatalf("asked about afresh: %d, %q, %d, %v; want 1, a, 7", id, note, extra, err)
		}
	})

	t.Run("a column is added, one format for all", func(t *testing.T) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "a int4, b int4", "1, 2", "SELECT * FROM TABLE WHERE a = $1", 1)
		mustExec(t, conn, "ALTER TABLE "+table+" ADD COLUMN c text DEFAULT 'x'")
		// One format covers however many columns there are, so the server runs
		// it, and the row has a value more than was remembered.
		for attempt := range 2 {
			var a, b int
			var c string
			if err := conn.QueryRow(ctx, sql, 1).Scan(&a, &b, &c); err != nil || a != 1 || b != 2 || c != "x" {
				t.Fatalf("attempt %d: %d, %d, %q, %v; want 1, 2, x", attempt, a, b, c, err)
			}
		}
	})

	t.Run("a result that Exec does not read", func(t *testing.T) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, note text", "1, 'a'", "SELECT id FROM TABLE WHERE note = $1", "a")
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN id TYPE int8")
		if _, err := conn.Exec(ctx, sql, "a"); err != nil {
			t.Fatal(err)
		}
		if conn.cache.get(sql) != nil {
			t.Error("a statement whose columns had changed was still remembered as it was")
		}
	})

	// In a batch, and in a window of a pipeline inside a transaction, a run of
	// the same statement shares one Parse, and so one description from the
	// server: only the first of them is told that the column changed.
	several := func(t *testing.T, run func(conn *Conn, queries []Query) (interface {
		QueryRow() *Row
		Close() error
	}, error)) {
		conn := liveConn(t, options)
		table, sql := staleTable(t, conn, "id int4, note text", "1, 'a'", "SELECT id FROM TABLE WHERE note = $1", "a")
		mustExec(t, conn, "ALTER TABLE "+table+" ALTER COLUMN id TYPE int8")
		mustExec(t, conn, "UPDATE "+table+" SET id = "+strconv.FormatInt(big, 10))
		queries := make([]Query, 3)
		for i := range queries {
			queries[i] = Query{SQL: sql, Arguments: []any{"a"}}
		}
		results, err := run(conn, queries)
		if err != nil {
			t.Fatal(err)
		}
		for i := range queries {
			var id int64
			if err := results.QueryRow().Scan(&id); err != nil || id != big {
				t.Errorf("statement %d: id = %d, %v; want %d", i, id, err, big)
			}
		}
		if err := results.Close(); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("a batch of one statement", func(t *testing.T) {
		several(t, func(conn *Conn, queries []Query) (interface {
			QueryRow() *Row
			Close() error
		}, error) {
			return conn.SendBatch(ctx, queries)
		})
	})
	t.Run("a pipeline in a transaction", func(t *testing.T) {
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
	t.Run("a pipeline", func(t *testing.T) {
		several(t, func(conn *Conn, queries []Query) (interface {
			QueryRow() *Row
			Close() error
		}, error) {
			return conn.SendPipeline(ctx, queries)
		})
	})
}

// Through a pooler in transaction mode, with clients that each run statements
// of their own, every answer must be the client's own. It is skipped unless
// POOLER_URL names such a pooler in front of the server of DATABASE_URL:
//
//	POOLER_URL='postgres://user:pass@127.0.0.1:6432/db' go test -run Pooler .
//
// What used to go wrong is told at TestLiveNothingOutlivesASyncWithoutStatementsKept,
// which holds the driver to it with no pooler at hand. This is the thing
// itself: a client's Bind reaching a server connection that another client's
// statement was parsed on.
func TestLiveOwnStatementsThroughAPooler(t *testing.T) {
	dsn := os.Getenv("POOLER_URL")
	if dsn == "" {
		t.Skip("POOLER_URL not set")
	}
	const clients, rounds = 12, 150
	for _, options := range notKept {
		var wrong, failed sync.Map
		var wg sync.WaitGroup
		for client := range clients {
			wg.Go(func() {
				conn, err := Connect(t.Context(), dsn, options)
				if err != nil {
					failed.Store(client, err)
					return
				}
				defer conn.Close(t.Context())
				// Each client's statements are its own, and give an answer
				// no other client's would.
				own := func(n int) string { return "SELECT $1::int4 + " + strconv.Itoa(1000*(client*10+n)) }
				want := func(n, arg int) int { return arg + 1000*(client*10+n) }
				for round := range rounds {
					var got int
					if err := conn.QueryRow(t.Context(), own(0), round).Scan(&got); err != nil {
						failed.Store(client, err)
						return
					}
					if got != want(0, round) {
						wrong.Store(client, got)
					}
					if round%10 != 0 {
						continue
					}
					queries := []Query{
						{SQL: own(1), Arguments: []any{round}},
						{SQL: own(2), Arguments: []any{round}},
						{SQL: own(1), Arguments: []any{round}},
					}
					for _, pipelined := range []bool{false, true} {
						var results interface {
							QueryRow() *Row
							Close() error
						}
						if pipelined {
							results, err = conn.SendPipeline(t.Context(), queries)
						} else {
							results, err = conn.SendBatch(t.Context(), queries)
						}
						if err != nil {
							failed.Store(client, err)
							return
						}
						for i, n := range []int{1, 2, 1} {
							if err := results.QueryRow().Scan(&got); err != nil {
								failed.Store(client, err)
							} else if got != want(n, round) {
								wrong.Store(client, [2]int{i, got})
							}
						}
						if err := results.Close(); err != nil {
							failed.Store(client, err)
							return
						}
					}
				}
			})
		}
		wg.Wait()
		failed.Range(func(client, err any) bool {
			t.Errorf("remembering %d: client %v: %v", options.DescriptionCacheCapacity, client, err)
			return true
		})
		wrong.Range(func(client, got any) bool {
			t.Errorf("remembering %d: client %v was given another statement's answer: %v", options.DescriptionCacheCapacity, client, got)
			return true
		})
	}
}
