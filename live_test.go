// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// Tests against a real server, skipped unless DATABASE_URL is set. Unit tests
// cannot see driver state; a result left unread or a statement cache gone
// stale shows only against PostgreSQL itself.
func liveConn(t *testing.T, options Options) *Conn {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	conn, err := Connect(t.Context(), dsn, options)
	if err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func liveTable(t *testing.T, conn *Conn) string {
	t.Helper()
	name := "caduceus_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := conn.Exec(t.Context(), "CREATE TABLE "+QuoteIdentifier(name)+" (id int PRIMARY KEY, note text)"); err != nil {
		t.Fatalf("CREATE TABLE = %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS "+QuoteIdentifier(name)) })
	return QuoteIdentifier(name)
}

func countRows(t *testing.T, conn *Conn, table string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count = %v", err)
	}
	return n
}

func TestLiveQueryExecAndRow(t *testing.T) {
	for _, options := range []Options{{}, {StatementCacheCapacity: -1}} {
		conn := liveConn(t, options)
		ctx := t.Context()
		table := liveTable(t, conn)

		tag, err := conn.Exec(ctx, "INSERT INTO "+table+" VALUES ($1, $2), ($3, $4)", 1, "one", 2, nil)
		if err != nil || tag != "INSERT 0 2" {
			t.Fatalf("Exec = %q, %v", tag, err)
		}
		rows, err := conn.Query(ctx, "SELECT id, note FROM "+table+" ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var id int
			var note *string
			if err := rows.Scan(&id, &note); err != nil {
				t.Fatal(err)
			}
			if note == nil {
				got = append(got, "null")
			} else {
				got = append(got, *note)
			}
		}
		rows.Close()
		if rows.Err() != nil || len(got) != 2 || got[0] != "one" || got[1] != "null" || rows.Tag() != "SELECT 2" {
			t.Fatalf("rows = %v, %v, tag %q", got, rows.Err(), rows.Tag())
		}

		var missing int
		if err := conn.QueryRow(ctx, "SELECT id FROM "+table+" WHERE id = $1", 99).Scan(&missing); !errors.Is(err, ErrNoRows) {
			t.Fatalf("QueryRow on nothing = %v, want ErrNoRows", err)
		}
		if conn.Busy() {
			t.Fatal("connection left busy")
		}
	}
}

// A result not read to the end must make the next call fail, not interleave
// with it, and closing it must free the connection.
func TestLiveUnreadResultKeepsTheConnectionBusy(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	rows, err := conn.Query(ctx, "SELECT generate_series(1, 1000)")
	if err != nil {
		t.Fatal(err)
	}
	rows.Next()
	if _, err := conn.Exec(ctx, "SELECT 1"); !errors.Is(err, ErrConnBusy) {
		t.Fatalf("Exec during an open result = %v, want ErrConnBusy", err)
	}
	rows.Close()
	rows.Close()
	if _, err := conn.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("Exec after Close = %v", err)
	}
}

func TestLiveTransactions(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)

	t.Run("commit", func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Begin(ctx); !errors.Is(err, ErrInTransaction) {
			t.Fatalf("Begin inside a transaction = %v, want ErrInTransaction", err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO "+table+" VALUES ($1, $2)", 10, "kept"); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		// The deferred-Rollback idiom: the second settle is a no-op.
		if err := tx.Rollback(ctx); !errors.Is(err, ErrTxClosed) {
			t.Fatalf("Rollback after Commit = %v, want ErrTxClosed", err)
		}
		if _, err := tx.Exec(ctx, "SELECT 1"); !errors.Is(err, ErrTxClosed) {
			t.Fatalf("Exec after Commit = %v", err)
		}
		if conn.TxStatus() != 'I' || countRows(t, conn, table) != 1 {
			t.Fatalf("status %c, rows %d", conn.TxStatus(), countRows(t, conn, table))
		}
	})

	t.Run("rollback", func(t *testing.T) {
		tx, _ := conn.Begin(ctx)
		_, _ = tx.Exec(ctx, "INSERT INTO "+table+" VALUES (11, 'gone')")
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if countRows(t, conn, table) != 1 {
			t.Fatal("rolled-back row is visible")
		}
	})

	t.Run("commit of a failed transaction rolls back", func(t *testing.T) {
		tx, _ := conn.Begin(ctx)
		_, _ = tx.Exec(ctx, "INSERT INTO "+table+" VALUES (12, 'x')")
		if _, err := tx.Exec(ctx, "INSERT INTO "+table+" VALUES (10, 'duplicate')"); err == nil {
			t.Fatal("duplicate key accepted")
		} else if pgErr := (*wire.Error)(nil); !errors.As(err, &pgErr) || pgErr.SQLState() != "23505" {
			t.Fatalf("error = %v, want SQLSTATE 23505", err)
		}
		if err := tx.Commit(ctx); !errors.Is(err, ErrTxCommitRollback) {
			t.Fatalf("Commit = %v, want ErrTxCommitRollback", err)
		}
		if conn.TxStatus() != 'I' || countRows(t, conn, table) != 1 {
			t.Fatal("failed transaction left something behind")
		}
	})

	t.Run("savepoint", func(t *testing.T) {
		tx, _ := conn.Begin(ctx)
		defer tx.Rollback(ctx)
		_, _ = tx.Exec(ctx, "INSERT INTO "+table+" VALUES (20, 'outer')")
		inner, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inner.Exec(ctx, "INSERT INTO "+table+" VALUES (10, 'duplicate')"); err == nil {
			t.Fatal("duplicate key accepted")
		}
		if err := inner.Rollback(ctx); err != nil {
			t.Fatalf("Rollback to savepoint = %v", err)
		}
		// The enclosing transaction survived the error inside the savepoint.
		kept, _ := tx.Begin(ctx)
		_, _ = kept.Exec(ctx, "INSERT INTO "+table+" VALUES (21, 'inner')")
		if err := kept.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if countRows(t, conn, table) != 3 {
			t.Fatalf("rows = %d, want 3", countRows(t, conn, table))
		}
	})

	t.Run("options", func(t *testing.T) {
		tx, err := conn.BeginTx(ctx, TxOptions{IsoLevel: Serializable, ReadOnly: true, Deferrable: true})
		if err != nil {
			t.Fatal(err)
		}
		var level string
		if err := tx.QueryRow(ctx, "SHOW transaction_isolation").Scan(&level); err != nil || level != "serializable" {
			t.Fatalf("isolation = %q, %v", level, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO "+table+" VALUES (30, 'ro')"); err == nil {
			t.Fatal("write accepted in a read-only transaction")
		}
		_ = tx.Rollback(ctx)
		if _, err := conn.BeginTx(ctx, TxOptions{IsoLevel: "READ COMMITTED; DROP TABLE x"}); !errors.Is(err, ErrIsoLevel) {
			t.Fatalf("injected isolation level = %v, want ErrIsoLevel", err)
		}
	})
}

func TestLiveBatchAndCopy(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)

	copied, err := conn.CopyFrom(ctx, []string{table[1 : len(table)-1]}, []string{"id", "note"},
		CopyFromRows([][]any{{1, "tab\there"}, {2, nil}, {3, `back\slash`}}))
	if err != nil || copied != 3 {
		t.Fatalf("CopyFrom = %d, %v", copied, err)
	}
	batch, err := conn.SendBatch(ctx, []Query{
		{SQL: "SELECT note FROM " + table + " WHERE id = $1", Arguments: []any{1}},
		{SQL: "SELECT note FROM " + table + " WHERE id = $1", Arguments: []any{3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tab\there", `back\slash`} {
		var note string
		if err := batch.QueryRow().Scan(&note); err != nil || note != want {
			t.Fatalf("batch row = %q, %v, want %q", note, err, want)
		}
	}
	if err := batch.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveNotification(t *testing.T) {
	listener := liveConn(t, Options{})
	sender := liveConn(t, Options{})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := listener.Exec(ctx, "LISTEN "+QuoteIdentifier("caduceus test")); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.Exec(ctx, "SELECT pg_notify($1, $2)", "caduceus test", "hello"); err != nil {
		t.Fatal(err)
	}
	n, err := listener.WaitForNotification(ctx)
	if err != nil || n.Channel != "caduceus test" || n.Payload != "hello" {
		t.Fatalf("notification = %+v, %v", n, err)
	}
}

// A context that ends mid-query cancels it on the server and closes the
// connection, since where its stream stands is no longer known. The caller
// must see the context's error and a closed connection, never a hang.
func TestLiveCancelledContext(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := conn.Exec(ctx, "SELECT pg_sleep(5)"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Exec = %v, want DeadlineExceeded", err)
	}
	if !conn.IsClosed() {
		t.Fatal("connection still open after its exchange was interrupted")
	}
	if _, err := conn.Exec(t.Context(), "SELECT 1"); !errors.Is(err, wire.ErrClosed) {
		t.Fatalf("Exec on the closed connection = %v, want wire.ErrClosed", err)
	}
}
