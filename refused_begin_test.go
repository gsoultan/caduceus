// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// onTheConnection are the ways a statement is run on a connection directly,
// around the Tx that is open on it. Each inserts the row it is given.
var onTheConnection = []struct {
	name string
	run  func(ctx context.Context, conn *Conn, table string, id int) error
}{
	{"Exec without arguments", func(ctx context.Context, conn *Conn, table string, id int) error {
		_, err := conn.Exec(ctx, "INSERT INTO "+table+" VALUES ("+strconv.Itoa(id)+", 'x')")
		return err
	}},
	{"Exec with arguments", func(ctx context.Context, conn *Conn, table string, id int) error {
		_, err := conn.Exec(ctx, "INSERT INTO "+table+" VALUES ($1, 'x')", id)
		return err
	}},
	{"Query", func(ctx context.Context, conn *Conn, table string, id int) error {
		rows, err := conn.Query(ctx, "INSERT INTO "+table+" VALUES ($1, 'x') RETURNING id", id)
		if err != nil {
			return err
		}
		rows.Close()
		return rows.Err()
	}},
	{"QueryRow", func(ctx context.Context, conn *Conn, table string, id int) error {
		var got int
		return conn.QueryRow(ctx, "INSERT INTO "+table+" VALUES ($1, 'x') RETURNING id", id).Scan(&got)
	}},
	{"SendBatch", func(ctx context.Context, conn *Conn, table string, id int) error {
		results, err := conn.SendBatch(ctx, []Query{{SQL: "INSERT INTO " + table + " VALUES ($1, 'x')", Arguments: []any{id}}})
		if err != nil {
			return err
		}
		return results.Close()
	}},
	{"SendPipeline", func(ctx context.Context, conn *Conn, table string, id int) error {
		results, err := conn.SendPipeline(ctx, []Query{{SQL: "INSERT INTO " + table + " VALUES ($1, 'x')", Arguments: []any{id}}})
		if err != nil {
			return err
		}
		return results.Close()
	}},
	{"CopyFrom", func(ctx context.Context, conn *Conn, table string, id int) error {
		_, err := conn.CopyFrom(ctx, []string{table[1 : len(table)-1]}, []string{"id", "note"}, CopyFromRows([][]any{{id, "x"}}))
		return err
	}},
}

// A transaction whose BEGIN the server refused was closed to its Tx and to
// nothing else. A statement run on the connection itself, which is how a batch
// or a copy is run inside a transaction and what the Tx says it may be used
// for, went to a server that had no transaction open: it ran on its own and
// stayed committed, and the Rollback that followed returned nil having undone
// nothing. A BEGIN sent ahead would have failed Begin, and there would have
// been no transaction to mistake.
func TestLiveNothingRunsOnTheConnectionAfterARefusedBegin(t *testing.T) {
	ctx := t.Context()
	observer := liveConn(t, Options{})
	for _, capacity := range []int{0, -1} {
		conn := liveConn(t, Options{StatementCacheCapacity: capacity})
		table := liveTable(t, conn)
		for _, begin := range refusedBegins {
			for _, settle := range []string{"Rollback", "Commit"} {
				name := begin.name + ", cache " + strconv.Itoa(capacity) + ", settled by " + settle
				tx, err := conn.Begin(ctx)
				if err != nil {
					t.Fatalf("%s: Begin = %v", name, err)
				}
				conn.pending = begin.sql
				var refusal *wire.Error
				if _, err := tx.Exec(ctx, "INSERT INTO "+table+" VALUES ($1, 'first')", 100); !errors.As(err, &refusal) || refusal.Code != begin.code {
					t.Fatalf("%s: first statement = %v, want the server's %s", name, err, begin.code)
				}

				if status := conn.TxStatus(); status != wire.TxFailed {
					t.Fatalf("%s: status = %c, want E: nothing may run until the transaction is settled", name, status)
				}
				for id, statement := range onTheConnection {
					if err := statement.run(ctx, conn, table, id); !errors.Is(err, ErrTxNotBegun) {
						t.Fatalf("%s: %s on the connection = %v, want ErrTxNotBegun", name, statement.name, err)
					}
				}
				if _, err := conn.Begin(ctx); !errors.Is(err, ErrInTransaction) {
					t.Fatalf("%s: Begin over the refused transaction = %v, want ErrInTransaction", name, err)
				}

				switch settle {
				case "Rollback":
					if err := tx.Rollback(ctx); err != nil {
						t.Fatalf("%s: Rollback = %v", name, err)
					}
				case "Commit":
					if err := tx.Commit(ctx); !errors.Is(err, ErrTxNotBegun) {
						t.Fatalf("%s: Commit = %v, want ErrTxNotBegun", name, err)
					}
				}
				if got := countRows(t, observer, table); got != 0 {
					t.Fatalf("%s: %d rows committed by statements run between Begin and %s", name, got, settle)
				}
				// Settled, the connection is anyone's again.
				if status := conn.TxStatus(); status != wire.TxIdle {
					t.Fatalf("%s: status once settled = %c, want I", name, status)
				}
				for id, statement := range onTheConnection {
					if err := statement.run(ctx, conn, table, id); err != nil {
						t.Fatalf("%s: %s once settled = %v", name, statement.name, err)
					}
				}
				if _, err := conn.Exec(ctx, "DELETE FROM "+table); err != nil {
					t.Fatalf("%s: DELETE = %v", name, err)
				}
			}
		}
	}
}

// Unwinding a connection for its next user has to lift the refusal too, or
// they would be refused in turn.
func TestLiveUnwindLiftsARefusedBegin(t *testing.T) {
	ctx := t.Context()
	conn := liveConn(t, Options{})
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conn.pending = refusedBegins[0].sql
	if _, err := tx.Exec(ctx, "SELECT $1::int4", 1); err == nil {
		t.Fatal("the first statement ran behind a BEGIN the server refuses")
	}
	if err := conn.Unwind(ctx); err != nil {
		t.Fatalf("Unwind = %v", err)
	}
	if status := conn.TxStatus(); status != wire.TxIdle {
		t.Fatalf("status = %c, want I", status)
	}
	if _, err := conn.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("Exec after Unwind = %v", err)
	}
	if err := tx.Rollback(ctx); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("Rollback on the Tx unwound = %v, want ErrTxClosed", err)
	}
}

// preparedOn lists the statements a session has prepared, asked over the
// wire so that the asking prepares none.
func preparedOn(t *testing.T, conn *Conn) string {
	t.Helper()
	result, err := conn.Wire().QueryText(t.Context(), "SELECT coalesce(string_agg(name, ',' ORDER BY name), '') FROM pg_prepared_statements")
	if err != nil || len(result.Rows) != 1 {
		t.Fatalf("pg_prepared_statements = %v", err)
	}
	return string(result.Rows[0][0])
}

// The Close of a statement evicted from the cache rides in front of the next
// statement described. It used to ride behind a transaction's BEGIN there, and
// a server that refuses the BEGIN skips everything up to the Sync: the
// statement stayed prepared on the server for the life of the connection,
// after the driver had forgotten it had it to close.
func TestLiveRefusedBeginStillClosesEvictedStatements(t *testing.T) {
	ctx := t.Context()
	for _, begin := range refusedBegins {
		conn := liveConn(t, Options{StatementCacheCapacity: 1})
		var n int
		for _, add := range []int{100, 200} {
			if err := conn.QueryRow(ctx, "SELECT $1::int4 + "+strconv.Itoa(add), 1).Scan(&n); err != nil {
				t.Fatal(err)
			}
		}
		if len(conn.closing) != 1 {
			t.Fatalf("%s: %d statements queued to be closed, want the one evicted", begin.name, len(conn.closing))
		}
		evicted := conn.closing[0]

		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conn.pending = begin.sql
		if err := tx.QueryRow(ctx, "SELECT $1::int4 + 300", 1).Scan(&n); err == nil {
			t.Fatalf("%s: the first statement ran behind a BEGIN the server refuses", begin.name)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("%s: Rollback = %v", begin.name, err)
		}
		// Whatever is still queued goes out with the next statement described.
		if err := conn.QueryRow(ctx, "SELECT $1::int4 + 400", 1).Scan(&n); err != nil {
			t.Fatalf("%s: the statement after = %v", begin.name, err)
		}
		if prepared := preparedOn(t, conn); containsName(prepared, evicted) {
			t.Fatalf("%s: %s was evicted and is still prepared on the server, which has %q", begin.name, evicted, prepared)
		}
	}
}

func containsName(list, name string) bool {
	for len(list) > 0 {
		var item string
		item, list, _ = cutComma(list)
		if item == name {
			return true
		}
	}
	return false
}

func cutComma(list string) (string, string, bool) {
	for i := range len(list) {
		if list[i] == ',' {
			return list[:i], list[i+1:], true
		}
	}
	return list, "", false
}

// A BEGIN the server refuses answers in the place of the statement behind it,
// and one way it is refused, serializable on a hot standby, has the SQLSTATE
// a cached plan gone stale has. The cached statement behind it, which never
// ran, was dropped for that and prepared again the next time.
func TestLiveRefusedBeginDoesNotEvictTheStatementBehindIt(t *testing.T) {
	ctx := t.Context()
	conn := liveConn(t, Options{})
	const sql = "SELECT $1::int4 + 7"
	var n int
	if err := conn.QueryRow(ctx, sql, 1).Scan(&n); err != nil {
		t.Fatal(err)
	}
	cached := conn.cache.get(sql)
	if cached == nil {
		t.Fatal("the statement was not cached")
	}
	refusedWhenRun := refusedBegins[1]
	if refusedWhenRun.code != "0A000" {
		t.Fatalf("this test wants the BEGIN refused with 0A000, not %s", refusedWhenRun.code)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conn.pending = refusedWhenRun.sql
	var refusal *wire.Error
	if err := tx.QueryRow(ctx, sql, 1).Scan(&n); !errors.As(err, &refusal) || refusal.Code != "0A000" {
		t.Fatalf("first statement = %v, want the BEGIN's 0A000", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if conn.cache.get(sql) != cached || len(conn.closing) != 0 {
		t.Fatalf("the statement behind the refused BEGIN was dropped from the cache (queued to close: %v)", conn.closing)
	}
}

// Unwind answered for a connection in the middle of a result as if it were
// idle. A transaction's BEGIN that went out with that result's statement is
// open on the server, but the status that would say so arrives with the end of
// the result: Unwind returned nil, having rolled back nothing, and a pool that
// trusted it would have handed on a connection with a transaction open.
func TestLiveUnwindRefusesABusyConnection(t *testing.T) {
	ctx := t.Context()
	conn := liveConn(t, Options{})
	const sql = "SELECT g FROM generate_series(1, $1::int4) g"
	var n int
	if err := conn.QueryRow(ctx, sql, 1).Scan(&n); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, sql, 3)
	if err != nil {
		t.Fatal(err)
	}
	if status := conn.TxStatus(); status != wire.TxActive {
		t.Fatalf("status with the BEGIN sent and its result unread = %c, want T", status)
	}
	if err := conn.Unwind(ctx); !errors.Is(err, ErrConnBusy) {
		t.Fatalf("Unwind in the middle of a result = %v, want ErrConnBusy", err)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("the result Unwind was refused over = %v", err)
	}
	if err := conn.Unwind(ctx); err != nil || conn.TxStatus() != wire.TxIdle {
		t.Fatalf("Unwind once the result is read = %v, status %c", err, conn.TxStatus())
	}
}

// A deadline that passed while rows were being read came out of Rows.Err, and
// out of a pipeline, as the socket's bare "i/o timeout": the rows kept the
// error of the read and dropped the one made of it, which wraps the context's.
// Exec, a batch and a copy reported the same event as the context's.
func TestLiveDeadlineWhileReadingRowsIsTheContexts(t *testing.T) {
	const slow = "SELECT g, pg_sleep(0.02) FROM generate_series(1, $1::int4) g"
	for _, kind := range []struct {
		name string
		run  func(ctx context.Context, conn *Conn) error
	}{
		{"Query", func(ctx context.Context, conn *Conn) error {
			rows, err := conn.Query(ctx, slow, 100)
			if err != nil {
				return err
			}
			for rows.Next() {
			}
			rows.Close()
			return rows.Err()
		}},
		{"QueryRow", func(ctx context.Context, conn *Conn) error {
			var n int
			return conn.QueryRow(ctx, "SELECT 1 FROM pg_sleep($1::float8)", 2.0).Scan(&n)
		}},
		{"SendPipeline", func(ctx context.Context, conn *Conn) error {
			results, err := conn.SendPipeline(ctx, []Query{{SQL: slow, Arguments: []any{100}}})
			if err != nil {
				return err
			}
			_, err = results.Exec()
			return errors.Join(err, results.Close())
		}},
		{"SendBatch", func(ctx context.Context, conn *Conn) error {
			results, err := conn.SendBatch(ctx, []Query{{SQL: slow, Arguments: []any{100}}})
			if err != nil {
				return err
			}
			_, err = results.Exec()
			return errors.Join(err, results.Close())
		}},
	} {
		t.Run(kind.name, func(t *testing.T) {
			conn := liveConn(t, Options{})
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
			defer cancel()
			err := kind.run(ctx, conn)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("got %v, want an error that is the context's deadline", err)
			}
			if !conn.IsClosed() {
				t.Fatal("the connection was left open in the middle of a result")
			}
		})
	}
}

// A result with another number of columns than its statement was described
// with is what a pooler produces when it prepares a statement again on a
// server where a table has changed. The row was refused, and the connection
// left open and not busy with the rest of the result still to come: the next
// statement read it as its own.
func TestLiveResultThatDoesNotMatchItsDescriptionClosesTheConnection(t *testing.T) {
	ctx := t.Context()
	conn := liveConn(t, Options{})
	const sql = "SELECT g FROM generate_series(1, $1::int4) g"
	var n int
	if err := conn.QueryRow(ctx, sql, 1).Scan(&n); err != nil {
		t.Fatal(err)
	}
	name := conn.cache.get(sql).name
	// Behind the driver's back, as a pooler does it.
	if _, err := conn.Wire().Exec(ctx, "DEALLOCATE "+name+"; PREPARE "+name+" (int4) AS SELECT g, g FROM generate_series(1, $1) g"); err != nil {
		t.Fatalf("replacing the statement = %v", err)
	}

	rows, err := conn.Query(ctx, sql, 5)
	if err != nil {
		t.Fatalf("Query = %v", err)
	}
	if rows.Next() {
		t.Fatal("a row with two columns was taken for a row of one")
	}
	rows.Close()
	if !errors.Is(rows.Err(), wire.ErrProtocol) {
		t.Fatalf("Err = %v, want ErrProtocol", rows.Err())
	}
	if !conn.IsClosed() {
		t.Fatal("the connection was left open with the rest of the result unread")
	}
}

// Committing a transaction on a connection that has since closed has to say
// so, even when its BEGIN was never sent: every statement run in it failed,
// and a nil from Commit would say they had been committed.
func TestLiveCommitOnAClosedConnectionBeforeBeginWasSent(t *testing.T) {
	ctx := t.Context()
	conn := liveConn(t, Options{})
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)
	if _, err := tx.Exec(ctx, "SELECT 1"); !errors.Is(err, wire.ErrClosed) {
		t.Fatalf("Exec = %v, want ErrClosed", err)
	}
	if err := tx.Commit(ctx); !errors.Is(err, wire.ErrClosed) {
		t.Fatalf("Commit = %v, want ErrClosed", err)
	}
}
