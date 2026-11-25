// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/gsoultan/caduceus/wire"
)

// backendState is what the server says conn's backend is doing, asked from
// another connection.
func backendState(t *testing.T, observer, conn *Conn) string {
	t.Helper()
	var state string
	err := observer.QueryRow(t.Context(), "SELECT state FROM pg_stat_activity WHERE pid = $1::int4", int32(conn.Wire().PID())).Scan(&state)
	if err != nil {
		t.Fatalf("pg_stat_activity = %v", err)
	}
	return state
}

// firstStatements are the ways a transaction can run its first statement,
// each inserting row 1 into the table. Each has to carry the BEGIN with it.
var firstStatements = []struct {
	name string
	run  func(t *testing.T, tx *Tx, table string) error
}{
	{"Exec without arguments", func(t *testing.T, tx *Tx, table string) error {
		_, err := tx.Exec(t.Context(), "INSERT INTO "+table+" VALUES (1, 'a')")
		return err
	}},
	{"Exec with arguments", func(t *testing.T, tx *Tx, table string) error {
		_, err := tx.Exec(t.Context(), "INSERT INTO "+table+" VALUES ($1, $2)", 1, "a")
		return err
	}},
	{"Query", func(t *testing.T, tx *Tx, table string) error {
		rows, err := tx.Query(t.Context(), "INSERT INTO "+table+" VALUES ($1, $2) RETURNING id", 1, "a")
		if err != nil {
			return err
		}
		for rows.Next() {
		}
		return rows.Err()
	}},
	{"QueryRow", func(t *testing.T, tx *Tx, table string) error {
		var id int
		return tx.QueryRow(t.Context(), "INSERT INTO "+table+" VALUES ($1, $2) RETURNING id", 1, "a").Scan(&id)
	}},
	{"SendBatch", func(t *testing.T, tx *Tx, table string) error {
		results, err := tx.SendBatch(t.Context(), []Query{{SQL: "INSERT INTO " + table + " VALUES ($1, $2)", Arguments: []any{1, "a"}}})
		if err != nil {
			return err
		}
		return results.Close()
	}},
	{"SendPipeline", func(t *testing.T, tx *Tx, table string) error {
		results, err := tx.SendPipeline(t.Context(), []Query{{SQL: "INSERT INTO " + table + " VALUES ($1, $2)", Arguments: []any{1, "a"}}})
		if err != nil {
			return err
		}
		return results.Close()
	}},
	{"CopyFrom", func(t *testing.T, tx *Tx, table string) error {
		_, err := tx.CopyFrom(t.Context(), []string{table[1 : len(table)-1]}, []string{"id", "note"}, CopyFromRows([][]any{{1, "a"}}))
		return err
	}},
	{"a savepoint", func(t *testing.T, tx *Tx, table string) error {
		inner, err := tx.Begin(t.Context())
		if err != nil {
			return err
		}
		if _, err := inner.Exec(t.Context(), "INSERT INTO "+table+" VALUES ($1, $2)", 1, "a"); err != nil {
			return err
		}
		return inner.Commit(t.Context())
	}},
}

// The BEGIN that Begin holds back goes with the first statement, whatever
// kind of statement that is. Were it lost, or sent after, the statement would
// commit on its own; so each is rolled back and must leave nothing, and then
// committed and must leave its row. Run with the statement cache on, where a
// statement seen before is bound straight away, and off, where every one is
// parsed first.
func TestLiveFirstStatementRunsInsideTheTransaction(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		conn := liveConn(t, Options{StatementCacheCapacity: capacity})
		table := liveTable(t, conn)
		for _, first := range firstStatements {
			for _, commit := range []bool{false, true, false} {
				tx, err := conn.Begin(t.Context())
				if err != nil {
					t.Fatalf("%s: Begin = %v", first.name, err)
				}
				if err := first.run(t, tx, table); err != nil {
					t.Fatalf("%s (cache %d): %v", first.name, capacity, err)
				}
				if status := conn.TxStatus(); status != wire.TxActive {
					t.Fatalf("%s: status after the first statement = %c, want T", first.name, status)
				}
				want := 0
				if commit {
					err, want = tx.Commit(t.Context()), 1
				} else {
					err = tx.Rollback(t.Context())
				}
				if err != nil {
					t.Fatalf("%s: settling = %v", first.name, err)
				}
				if got := countRows(t, conn, table); got != want {
					t.Fatalf("%s (cache %d, commit %v): %d rows, want %d", first.name, capacity, commit, got, want)
				}
				if _, err := conn.Exec(t.Context(), "TRUNCATE "+table); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

// Begin sends nothing: the server sees no transaction until the first
// statement, and a transaction that runs none is settled without a round trip.
func TestLiveBeginIsNotSentUntilTheFirstStatement(t *testing.T) {
	conn, observer := liveConn(t, Options{}), liveConn(t, Options{})
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status := conn.TxStatus(); status != wire.TxActive {
		t.Fatalf("status after Begin = %c, want T: a pool must not take the connection back as idle", status)
	}
	if _, err := conn.Begin(t.Context()); !errors.Is(err, ErrInTransaction) {
		t.Fatalf("second Begin = %v, want ErrInTransaction", err)
	}
	if state := backendState(t, observer, conn); state != "idle" {
		t.Fatalf("server state after Begin = %q, want idle", state)
	}
	if _, err := tx.Exec(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if state := backendState(t, observer, conn); state != "idle in transaction" {
		t.Fatalf("server state after the first statement = %q, want idle in transaction", state)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}

	for _, settle := range []func(*Tx) error{
		func(tx *Tx) error { return tx.Commit(t.Context()) },
		func(tx *Tx) error { return tx.Rollback(t.Context()) },
	} {
		empty, err := conn.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := settle(empty); err != nil {
			t.Fatalf("settling an empty transaction = %v", err)
		}
		if status := conn.TxStatus(); status != wire.TxIdle {
			t.Fatalf("status after an empty transaction = %c, want I", status)
		}
		if err := empty.Rollback(t.Context()); !errors.Is(err, ErrTxClosed) {
			t.Fatalf("second settling = %v, want ErrTxClosed", err)
		}
	}
	// The connection must be clean for whoever uses it next: nothing left
	// waiting to be prepended to their statement.
	if _, err := conn.Exec(t.Context(), "SELECT 1"); err != nil || conn.TxStatus() != wire.TxIdle {
		t.Fatalf("after empty transactions: %v, status %c", err, conn.TxStatus())
	}
}

// refusedBegins stand in for a BEGIN the server refuses, which a real one is
// for characteristics the server cannot give, such as serializable on a hot
// standby. One is refused when it is parsed and one when it is run, which the
// server answers at different points of the exchange.
var refusedBegins = []struct {
	name, sql, code string
}{
	{"refused when parsed", "BEGIN ISOLATION LEVEL unheard_of", "42601"},
	{"refused when run", "DO $$ BEGIN RAISE EXCEPTION 'no transaction here' USING ERRCODE = '0A000'; END $$", "0A000"},
}

// A BEGIN the server refuses must take its statement down with it. The
// statement running anyway would commit on its own, outside any transaction:
// the worst outcome there is for code that asked for one. Run with the
// statement cache on, where the BEGIN shares a Sync with the statement itself
// or with a batch, and off, where it shares one with what describes them.
func TestLiveRefusedBeginRunsNothing(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		conn := liveConn(t, Options{StatementCacheCapacity: capacity})
		table := liveTable(t, conn)
		// Warm the cache, so that with it on the insert is bound without a parse.
		if _, err := conn.Exec(t.Context(), "INSERT INTO "+table+" VALUES ($1, $2)", 0, "warm"); err != nil {
			t.Fatal(err)
		}
		for _, begin := range refusedBegins {
			for _, first := range firstStatements {
				name := first.name + ", " + begin.name + ", cache " + strconv.Itoa(capacity)
				tx, err := conn.Begin(t.Context())
				if err != nil {
					t.Fatalf("%s: Begin = %v", name, err)
				}
				conn.pending = begin.sql
				var refusal *wire.Error
				if err := first.run(t, tx, table); !errors.As(err, &refusal) || refusal.Code != begin.code {
					t.Fatalf("%s: first statement = %v, want the server's %s", name, err, begin.code)
				}
				// To the caller the transaction has failed; the server has none.
				if status := conn.TxStatus(); status != wire.TxFailed || conn.Wire().TxStatus() != wire.TxIdle {
					t.Fatalf("%s: status = %c and the server's %c, want E and I", name, status, conn.Wire().TxStatus())
				}
				if _, err := tx.Exec(t.Context(), "INSERT INTO "+table+" VALUES (2, 'b')"); !errors.Is(err, ErrTxNotBegun) {
					t.Fatalf("%s: second statement = %v, want ErrTxNotBegun", name, err)
				}
				if err := tx.Commit(t.Context()); !errors.Is(err, ErrTxNotBegun) {
					t.Fatalf("%s: Commit = %v, want ErrTxNotBegun", name, err)
				}
				if status := conn.TxStatus(); status != wire.TxIdle {
					t.Fatalf("%s: status once settled = %c, want I", name, status)
				}
				if got := countRows(t, conn, table); got != 1 {
					t.Fatalf("%s: %d rows, want only the warm-up row: a statement ran outside the transaction", name, got)
				}
			}
		}
		// The next transaction is unaffected.
		tx, err := conn.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), "INSERT INTO "+table+" VALUES ($1, $2)", 3, "c"); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil || countRows(t, conn, table) != 2 {
			t.Fatalf("transaction after a refused one: %v, %d rows", err, countRows(t, conn, table))
		}
	}
}

// A first statement without arguments shares its Query string with BEGIN, and
// the server parses a string whole before it runs any of it. One it could not
// parse took the BEGIN down with it and left a session with no transaction,
// where a BEGIN sent ahead leaves one open and failed: what the caller ran
// next on the connection ran on its own and stayed committed. So a string
// refused whole is sent again as its two parts, and the caller finds what it
// always found: the statement's own error at its own position, a failed
// transaction that takes nothing more, and a Rollback that ends it.
func TestLiveFirstStatementThatCannotBeParsed(t *testing.T) {
	ctx := t.Context()
	observer := liveConn(t, Options{})
	conn, trips := countedConn(t, Options{})
	table := liveTable(t, conn)
	aborted := func(name string, err error) {
		t.Helper()
		var failure *wire.Error
		if !errors.As(err, &failure) || failure.Code != "25P02" {
			t.Fatalf("%s = %v, want the server's 25P02: the transaction is open and failed", name, err)
		}
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	trips()
	_, err = tx.Exec(ctx, "SELEC 1")
	var failure *wire.Error
	if !errors.As(err, &failure) || failure.Code != "42601" || failure.Position != 1 {
		t.Fatalf("Exec = %v (position %d), want 42601 at position 1", err, failure.Position)
	}
	if got := trips(); got != 3 {
		t.Fatalf("%d round trips, want 3: the string, then BEGIN and the statement apart", got)
	}
	if status := conn.TxStatus(); status != wire.TxFailed || conn.Wire().TxStatus() != wire.TxFailed {
		t.Fatalf("status = %c and the server's %c, want E for both", status, conn.Wire().TxStatus())
	}
	_, err = tx.Exec(ctx, "SELECT 1")
	aborted("the second statement", err)
	for id, statement := range onTheConnection {
		aborted(statement.name+" on the connection", statement.run(ctx, conn, table, id))
	}
	if err := tx.Rollback(ctx); err != nil || conn.TxStatus() != wire.TxIdle {
		t.Fatalf("Rollback = %v, status %c", err, conn.TxStatus())
	}
	if got := countRows(t, observer, table); got != 0 {
		t.Fatalf("%d rows committed by statements run inside the failed transaction", got)
	}

	// One the server can parse still takes the one round trip.
	tx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	trips()
	if _, err := tx.Exec(ctx, "SELECT 1"); err != nil || trips() != 1 {
		t.Fatalf("a first statement the server can parse: %v, and not in one round trip", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

// A first statement the server rejects leaves the transaction open and
// failed, as it would behind a BEGIN sent on its own, and the position in the
// error is counted in the caller's statement, not in the BEGIN put before it.
func TestLiveFirstStatementFailure(t *testing.T) {
	conn := liveConn(t, Options{})
	for _, args := range [][]any{nil, {1}} {
		tx, err := conn.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		sql := "SELECT 1 FROM caduceus_no_such_table"
		if args != nil {
			sql += " WHERE 1 = $1"
		}
		_, err = tx.Exec(t.Context(), sql, args...)
		var failure *wire.Error
		if !errors.As(err, &failure) || failure.Code != "42P01" || failure.Position != 15 {
			t.Fatalf("Exec = %v (position %d), want 42P01 at position 15", err, failure.Position)
		}
		if status := conn.TxStatus(); status != wire.TxFailed {
			t.Fatalf("status = %c, want E", status)
		}
		if err := tx.Rollback(t.Context()); err != nil || conn.TxStatus() != wire.TxIdle {
			t.Fatalf("Rollback = %v, status %c", err, conn.TxStatus())
		}
	}
}

// The point of holding BEGIN back is the round trip it saves, and nothing
// else here would notice it being sent alone again: every other test passes
// either way. So the round trips are counted. With the statements known, a
// transaction is its first statement and its COMMIT; a statement the server
// has not seen is described first, and BEGIN goes with that. Only a pipeline
// of known statements still sends BEGIN by itself. It is the same whether the
// statements are kept on the server or only what the server said of them is
// remembered; a connection that remembers nothing describes every time.
func TestLiveTransactionRoundTrips(t *testing.T) {
	for _, options := range []Options{
		{},
		{StatementCacheCapacity: -1},
		{StatementCacheCapacity: -1, DescriptionCacheCapacity: -1},
	} {
		capacity := strconv.Itoa(options.StatementCacheCapacity) + "/" + strconv.Itoa(options.DescriptionCacheCapacity)
		conn, trips := countedConn(t, options)
		table := liveTable(t, conn)
		ctx := t.Context()
		insert := "INSERT INTO " + table + " VALUES ($1, $2)"
		id := 0
		one := func() []Query {
			id++
			return []Query{{SQL: insert, Arguments: []any{id, "a"}}}
		}
		// described is the round trip that describes a statement, which is
		// made every time by a connection that remembers nothing of its
		// statements and never, for these, by one that does.
		described := 0
		if options.DescriptionCacheCapacity < 0 {
			described = 1
		}
		if _, err := conn.Exec(ctx, insert, 0, "warm"); err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			name string
			want int
			run  func(tx *Tx) error
		}{
			{"no statement", 0, nil},
			{"Exec without arguments", 2, func(tx *Tx) error {
				_, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE id < 0")
				return err
			}},
			{"Exec with arguments", 2 + described, func(tx *Tx) error {
				_, err := tx.Exec(ctx, insert, one()[0].Arguments...)
				return err
			}},
			{"QueryRow", 2 + described, func(tx *Tx) error {
				var note string
				return tx.QueryRow(ctx, insert+" RETURNING note", one()[0].Arguments...).Scan(&note)
			}},
			{"SendBatch", 2 + described, func(tx *Tx) error {
				results, err := tx.SendBatch(ctx, one())
				if err != nil {
					return err
				}
				return results.Close()
			}},
			{"SendPipeline", 3, func(tx *Tx) error {
				results, err := tx.SendPipeline(ctx, one())
				if err != nil {
					return err
				}
				return results.Close()
			}},
			{"CopyFrom", 3, func(tx *Tx) error {
				id++
				_, err := tx.CopyFrom(ctx, []string{table[1 : len(table)-1]}, []string{"id", "note"}, CopyFromRows([][]any{{id, "a"}}))
				return err
			}},
		}
		// Each is run twice and the second counted, so that its statement is one
		// the connection has seen.
		for _, c := range cases {
			for attempt := range 2 {
				trips()
				tx, err := conn.Begin(ctx)
				if err != nil {
					t.Fatalf("%s: Begin = %v", c.name, err)
				}
				if c.run != nil {
					if err := c.run(tx); err != nil {
						t.Fatalf("%s (cache %s): %v", c.name, capacity, err)
					}
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatalf("%s: Commit = %v", c.name, err)
				}
				if got := trips(); attempt == 1 && got != c.want {
					t.Errorf("%s (cache %s): %d round trips, want %d", c.name, capacity, got, c.want)
				}
			}
		}
		// A statement the connection has not seen is described first, whatever
		// it keeps, and BEGIN goes with the describe.
		for i, kind := range []string{"Exec", "SendBatch", "SendPipeline"} {
			fresh := insert + " /* " + strconv.Itoa(i) + " */"
			id++
			trips()
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "Exec":
				_, err = tx.Exec(ctx, fresh, id, "a")
			case "SendBatch":
				var results *BatchResults
				if results, err = tx.SendBatch(ctx, []Query{{SQL: fresh, Arguments: []any{id, "a"}}}); err == nil {
					err = results.Close()
				}
			case "SendPipeline":
				var results *PipelineResults
				if results, err = tx.SendPipeline(ctx, []Query{{SQL: fresh, Arguments: []any{id, "a"}}}); err == nil {
					err = results.Close()
				}
			}
			if err == nil {
				err = tx.Commit(ctx)
			}
			if err != nil {
				t.Fatalf("%s of a new statement (cache %s): %v", kind, capacity, err)
			}
			if got := trips(); got != 3 {
				t.Errorf("%s of a new statement (cache %s): %d round trips, want 3", kind, capacity, got)
			}
		}
		// Everything counted was also done: a row for each of the inserts.
		if got := countRows(t, conn, table); got != id+1 {
			t.Fatalf("cache %s: %d rows, want %d", capacity, got, id+1)
		}
	}
}

// A Tx that outlives its transaction must not act on the one after it. A
// leftover Tx used to be told from a live one only by its own settling, so
// one left behind could take a later transaction's unsent BEGIN away with its
// Rollback, after which that transaction's statements committed one by one;
// or it could run its own statements inside the later transaction.
func TestLiveSpentTransactionCannotTouchALaterOne(t *testing.T) {
	conn := liveConn(t, Options{})
	table := liveTable(t, conn)
	ctx := t.Context()
	spent := func(name string, tx *Tx) {
		t.Helper()
		if _, err := tx.Exec(ctx, "INSERT INTO "+table+" VALUES (99, 'stale')"); !errors.Is(err, ErrTxClosed) {
			t.Fatalf("%s: Exec = %v, want ErrTxClosed", name, err)
		}
		if _, err := tx.Begin(ctx); !errors.Is(err, ErrTxClosed) {
			t.Fatalf("%s: Begin = %v, want ErrTxClosed", name, err)
		}
		if err := tx.Rollback(ctx); !errors.Is(err, ErrTxClosed) {
			t.Fatalf("%s: Rollback = %v, want ErrTxClosed", name, err)
		}
		if err := tx.Commit(ctx); !errors.Is(err, ErrTxClosed) {
			t.Fatalf("%s: Commit = %v, want ErrTxClosed", name, err)
		}
	}

	// Left behind by a caller that ended its transaction in SQL.
	stale, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stale.Exec(ctx, "INSERT INTO "+table+" VALUES (1, 'a')"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}
	later, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	spent("a Tx ended in SQL", stale)
	if _, err := later.Exec(ctx, "INSERT INTO "+table+" VALUES (2, 'b')"); err != nil {
		t.Fatal(err)
	}
	if status := conn.TxStatus(); status != wire.TxActive {
		t.Fatalf("status = %c, want T: the later transaction lost its BEGIN", status)
	}
	if err := later.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	// A savepoint that outlives the transaction around it.
	outer, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := outer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := outer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	spent("a savepoint of a settled transaction", inner)

	// Left unsettled after the server refused its BEGIN.
	refused, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conn.pending = refusedBegins[0].sql
	if _, err := refused.Exec(ctx, "SELECT 1"); err == nil {
		t.Fatal("a refused BEGIN reported nothing")
	}
	// Nothing begins over it until it is settled or, as here, a pool has
	// taken the connection back.
	if _, err := conn.Begin(ctx); !errors.Is(err, ErrInTransaction) {
		t.Fatalf("Begin over a refused transaction = %v, want ErrInTransaction", err)
	}
	if err := conn.Unwind(ctx); err != nil {
		t.Fatalf("Unwind = %v", err)
	}
	later, err = conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	spent("a Tx never begun", refused)
	if _, err := later.Exec(ctx, "INSERT INTO "+table+" VALUES (3, 'c')"); err != nil {
		t.Fatal(err)
	}
	if err := later.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, conn, table); got != 1 {
		t.Fatalf("%d rows, want the one row committed", got)
	}
}

// Unwind is a pool taking a connection back: whatever transaction its caller
// left is gone, and so is every Tx that could still reach the connection. A
// transaction that never reached the server costs nothing to unwind.
func TestLiveUnwind(t *testing.T) {
	conn, trips := countedConn(t, Options{})
	table := liveTable(t, conn)
	ctx := t.Context()
	unwound := func(name string, tx *Tx, want int) {
		t.Helper()
		trips()
		if err := conn.Unwind(ctx); err != nil {
			t.Fatalf("%s: Unwind = %v", name, err)
		}
		if got := trips(); got != want {
			t.Fatalf("%s: Unwind took %d round trips, want %d", name, got, want)
		}
		if status := conn.TxStatus(); status != wire.TxIdle {
			t.Fatalf("%s: status = %c, want I", name, status)
		}
		if tx == nil {
			return
		}
		if _, err := tx.Exec(ctx, "INSERT INTO "+table+" VALUES (99, 'stale')"); !errors.Is(err, ErrTxClosed) {
			t.Fatalf("%s: Exec on its Tx = %v, want ErrTxClosed", name, err)
		}
		if err := tx.Commit(ctx); !errors.Is(err, ErrTxClosed) {
			t.Fatalf("%s: Commit on its Tx = %v, want ErrTxClosed", name, err)
		}
	}

	unwound("nothing open", nil, 0)

	unsent, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	unwound("a transaction not yet sent", unsent, 0)
	// The next caller's statement must not find that BEGIN in front of it.
	if _, err := conn.Exec(ctx, "INSERT INTO "+table+" VALUES (1, 'a')"); err != nil || conn.TxStatus() != wire.TxIdle {
		t.Fatalf("statement after Unwind: %v, status %c", err, conn.TxStatus())
	}

	open, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := open.Exec(ctx, "INSERT INTO "+table+" VALUES (2, 'b')"); err != nil {
		t.Fatal(err)
	}
	unwound("an open transaction", open, 1)

	failed, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failed.Exec(ctx, "SELECT 1 FROM caduceus_no_such_table"); err == nil {
		t.Fatal("a missing table reported nothing")
	}
	unwound("a failed transaction", failed, 1)

	refused, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conn.pending = refusedBegins[0].sql
	if _, err := refused.Exec(ctx, "SELECT 1"); err == nil {
		t.Fatal("a refused BEGIN reported nothing")
	}
	unwound("a transaction never begun", refused, 0)

	if got := countRows(t, conn, table); got != 1 {
		t.Fatalf("%d rows, want the one inserted outside a transaction", got)
	}
}

// On a hot standby the server refuses a serializable BEGIN: the refusal the
// tests above stand in for. Set STANDBY_URL to a hot standby to see the real
// one. Every first statement here is a read, which a standby would run;
// behind the refused BEGIN it must not, or the caller would be handed, as if
// read serializably, rows it read under no such guarantee.
func TestLiveSerializableOnAStandby(t *testing.T) {
	dsn := os.Getenv("STANDBY_URL")
	if dsn == "" {
		t.Skip("STANDBY_URL not set")
	}
	ctx := t.Context()
	reads := []struct {
		name string
		run  func(tx *Tx) error
	}{
		{"Exec without arguments", func(tx *Tx) error {
			_, err := tx.Exec(ctx, "SELECT 1")
			return err
		}},
		{"Exec with arguments", func(tx *Tx) error {
			_, err := tx.Exec(ctx, "SELECT $1::int4", 1)
			return err
		}},
		{"QueryRow", func(tx *Tx) error {
			var n int
			return tx.QueryRow(ctx, "SELECT $1::int4 + 1", 1).Scan(&n)
		}},
		{"SendBatch", func(tx *Tx) error {
			results, err := tx.SendBatch(ctx, []Query{{SQL: "SELECT $1::int4 + 2", Arguments: []any{1}}})
			if err != nil {
				return err
			}
			return results.Close()
		}},
		{"SendPipeline", func(tx *Tx) error {
			results, err := tx.SendPipeline(ctx, []Query{{SQL: "SELECT $1::int4 + 3", Arguments: []any{1}}})
			if err != nil {
				return err
			}
			return results.Close()
		}},
	}
	for _, capacity := range []int{0, -1} {
		conn, err := Connect(ctx, dsn, Options{StatementCacheCapacity: capacity})
		if err != nil {
			t.Fatalf("Connect() = %v", err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })
		// Twice, so that with a cache the second round binds statements the
		// first one's transaction, begun as the standby allows, made known.
		for round := range 2 {
			for _, read := range reads {
				name := read.name + ", cache " + strconv.Itoa(capacity) + ", round " + strconv.Itoa(round)
				tx, err := conn.BeginTx(ctx, TxOptions{IsoLevel: Serializable})
				if err != nil {
					t.Fatalf("%s: BeginTx = %v", name, err)
				}
				var refusal *wire.Error
				if err := read.run(tx); !errors.As(err, &refusal) || refusal.Code != "0A000" {
					t.Fatalf("%s: first statement = %v, want the standby's 0A000", name, err)
				}
				if status := conn.TxStatus(); status != wire.TxFailed {
					t.Fatalf("%s: status = %c, want E", name, status)
				}
				if _, err := tx.Exec(ctx, "SELECT 1"); !errors.Is(err, ErrTxNotBegun) {
					t.Fatalf("%s: second statement = %v, want ErrTxNotBegun", name, err)
				}
				if _, err := conn.Exec(ctx, "SELECT 1"); !errors.Is(err, ErrTxNotBegun) {
					t.Fatalf("%s: a statement on the connection = %v, want ErrTxNotBegun", name, err)
				}
				if err := tx.Rollback(ctx); err != nil || conn.TxStatus() != wire.TxIdle {
					t.Fatalf("%s: Rollback = %v, status %c", name, err, conn.TxStatus())
				}
			}
			allowed, err := conn.BeginTx(ctx, TxOptions{IsoLevel: RepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, read := range reads {
				if err := read.run(allowed); err != nil {
					t.Fatalf("%s in a transaction the standby allows: %v", read.name, err)
				}
			}
			if err := allowed.Commit(ctx); err != nil {
				t.Fatalf("Commit = %v", err)
			}
		}
	}
}
