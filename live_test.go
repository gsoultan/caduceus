// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
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

// A pipeline's statements are independent: a duplicate key in the middle
// fails that statement alone, unlike a batch, where it skips the rest.
func TestLivePipelineIsolatesFailures(t *testing.T) {
	for _, options := range []Options{{}, {StatementCacheCapacity: -1}} {
		conn := liveConn(t, options)
		ctx := t.Context()
		table := liveTable(t, conn)
		insert := "INSERT INTO " + table + " VALUES ($1, $2)"

		queries := []Query{
			{SQL: insert, Arguments: []any{1, "a"}},
			{SQL: insert, Arguments: []any{1, "duplicate"}},
			{SQL: insert, Arguments: []any{make(chan int), "b"}},
			{SQL: insert, Arguments: []any{2, "c"}},
			{SQL: "SELECT count(*) FROM " + table},
		}
		results, err := conn.SendPipeline(ctx, queries)
		if err != nil {
			t.Fatal(err)
		}
		if tag, err := results.Exec(); err != nil || tag != "INSERT 0 1" {
			t.Fatalf("first insert = %q, %v", tag, err)
		}
		if _, err := results.Exec(); err == nil {
			t.Fatal("duplicate key accepted")
		}
		var argErr *ArgumentError
		if _, err := results.Exec(); !errors.As(err, &argErr) {
			t.Fatalf("unencodable argument = %v, want an ArgumentError", err)
		}
		if _, err := results.Exec(); err != nil {
			t.Fatalf("insert after the failures = %v", err)
		}
		var n int
		if err := results.QueryRow().Scan(&n); err != nil || n != 2 {
			t.Fatalf("count = %d, %v", n, err)
		}
		if err := results.Close(); err == nil {
			t.Fatal("Close must report the pipeline's first error")
		}
		if conn.Busy() || conn.TxStatus() != 'I' {
			t.Fatalf("busy %v, status %c after the pipeline", conn.Busy(), conn.TxStatus())
		}
		if _, err := conn.Exec(ctx, "SELECT 1"); err != nil {
			t.Fatalf("connection unusable after the pipeline: %v", err)
		}
	}
}

// Many windows' worth of statements, closed without reading a single result:
// Close must still send and run every one, and leave the connection idle.
func TestLivePipelineCloseRunsEverything(t *testing.T) {
	for _, options := range []Options{{}, {StatementCacheCapacity: -1}} {
		conn := liveConn(t, options)
		ctx := t.Context()
		table := liveTable(t, conn)
		const count = 20000
		queries := make([]Query, count)
		for i := range queries {
			queries[i] = Query{SQL: "INSERT INTO " + table + " VALUES ($1, $2)", Arguments: []any{i, "row"}}
		}
		results, err := conn.SendPipeline(ctx, queries)
		if err != nil {
			t.Fatal(err)
		}
		if err := results.Close(); err != nil {
			t.Fatal(err)
		}
		if err := results.Close(); err != nil {
			t.Fatalf("second Close = %v", err)
		}
		if got := countRows(t, conn, table); got != count {
			t.Fatalf("rows = %d, want %d", got, count)
		}
	}
}

func TestLivePipelineInATransaction(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	results, err := tx.SendPipeline(ctx, []Query{
		{SQL: "INSERT INTO " + table + " VALUES (1, 'a')"},
		{SQL: "INSERT INTO " + table + " VALUES (2, 'b')"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if countRows(t, conn, table) != 0 {
		t.Fatal("rolled-back pipeline left rows behind")
	}
}

// Inside a transaction a pipeline's window shares one Sync. A failure must
// surface on the statement that caused it, every later statement must report
// an error rather than hang waiting for replies the server never sends, and
// the connection must come out in step with the server.
func TestLivePipelineFailureInsideATransaction(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)
	insert := "INSERT INTO " + table + " VALUES ($1, $2)"

	// Enough statements for several windows, with the failure in the first.
	const count, bad = 6000, 5
	queries := make([]Query, count)
	for i := range queries {
		queries[i] = Query{SQL: insert, Arguments: []any{i, "row"}}
	}
	queries[bad].Arguments = []any{0, "duplicate of the first"}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	results, err := tx.SendPipeline(ctx, queries)
	if err != nil {
		t.Fatal(err)
	}
	for i := range count {
		_, err := results.Exec()
		switch {
		case i < bad && err != nil:
			t.Fatalf("statement %d before the failure = %v", i, err)
		case i == bad:
			var pgErr *wire.Error
			if !errors.As(err, &pgErr) || pgErr.SQLState() != "23505" {
				t.Fatalf("failing statement = %v, want SQLSTATE 23505", err)
			}
		case i > bad && err == nil:
			t.Fatalf("statement %d after the failure reported success", i)
		}
	}
	if err := results.Close(); err == nil {
		t.Fatal("Close must report the failure")
	}
	if conn.Busy() || conn.TxStatus() != 'E' {
		t.Fatalf("busy %v, status %c; want an idle connection in a failed transaction", conn.Busy(), conn.TxStatus())
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, conn, table); got != 0 {
		t.Fatalf("rows = %d after rolling back", got)
	}
}

// An argument that cannot be encoded is never sent, so inside a transaction
// it fails alone and the transaction carries on, as it does for a lone Exec.
func TestLivePipelineBadArgumentInsideATransaction(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)
	insert := "INSERT INTO " + table + " VALUES ($1, $2)"
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	results, err := tx.SendPipeline(ctx, []Query{
		{SQL: insert, Arguments: []any{1, "a"}},
		{SQL: insert, Arguments: []any{make(chan int), "b"}},
		{SQL: insert, Arguments: []any{make(chan int), "c"}},
		{SQL: insert, Arguments: []any{2, "d"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var argErr *ArgumentError
	if err := results.Close(); !errors.As(err, &argErr) {
		t.Fatalf("Close = %v, want the ArgumentError", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, conn, table); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}
}

// copyTable creates a table of the given column definitions and returns its
// unquoted name, which is the form CopyFrom takes.
func copyTable(t *testing.T, conn *Conn, columns string) string {
	t.Helper()
	name := "caduceus_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := conn.Exec(t.Context(), "CREATE TABLE "+QuoteIdentifier(name)+" ("+columns+")"); err != nil {
		t.Fatalf("CREATE TABLE = %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS "+QuoteIdentifier(name)) })
	return name
}

// Every type COPY sends in binary, with a NULL in each column, read back
// through a cast to text so the comparison is with what the server stored
// rather than with this driver's own decoding of it.
func TestLiveCopyBinaryTypes(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := copyTable(t, conn, `b bool, i2 int2, i4 int4, i8 int8, f4 float4, f8 float8, t text,
		v varchar(8), c char(3), raw bytea, u uuid, d date, ts timestamp, tz timestamptz, iv interval, j json, jb jsonb`)
	columns := []string{"b", "i2", "i4", "i8", "f4", "f8", "t", "v", "c", "raw", "u", "d", "ts", "tz", "iv", "j", "jb"}
	when := time.Date(2026, 3, 4, 5, 6, 7, 891000000, time.UTC)
	rows := [][]any{
		{true, int16(-2), int32(4), int64(1) << 40, float32(1.5), 2.25, "tab\there\\\nline", "varchar", "ab",
			[]byte{0, 1, 0xff}, [16]byte{0xde, 0xad, 0xbe, 0xef}, when, when, when, 90 * time.Minute,
			`{"a": 1}`, map[string]any{"b": []int{1, 2}}},
		make([]any, len(columns)),
	}
	copied, err := conn.CopyFrom(ctx, []string{table}, columns, CopyFromRows(rows))
	if err != nil || copied != 2 {
		t.Fatalf("CopyFrom = %d, %v", copied, err)
	}

	if _, err := conn.Exec(ctx, "SET TIME ZONE 'UTC'"); err != nil {
		t.Fatal(err)
	}
	want := []string{"true", "-2", "4", "1099511627776", "1.5", "2.25", "tab\there\\\nline", "varchar", "ab",
		`\x0001ff`, "deadbeef-0000-0000-0000-000000000000", "2026-03-04", "2026-03-04 05:06:07.891",
		"2026-03-04 05:06:07.891+00", "01:30:00", `{"a": 1}`, `{"b": [1, 2]}`}
	got := make([]*string, len(columns))
	targets := make([]any, len(columns))
	for i := range got {
		targets[i] = &got[i]
	}
	query := "SELECT b::text, i2::text, i4::text, i8::text, f4::text, f8::text, t, v::text, c::text, raw::text, u::text, d::text, ts::text, tz::text, iv::text, j::text, jb::text FROM " + QuoteIdentifier(table)
	if err := conn.QueryRow(ctx, query+" WHERE b IS NOT NULL").Scan(targets...); err != nil {
		t.Fatal(err)
	}
	for i, column := range columns {
		if got[i] == nil || *got[i] != want[i] {
			t.Errorf("column %s = %v, want %q", column, got[i], want[i])
		}
	}
	if err := conn.QueryRow(ctx, query+" WHERE b IS NULL").Scan(targets...); err != nil {
		t.Fatal(err)
	}
	for i, column := range columns {
		if got[i] != nil {
			t.Errorf("column %s = %q, want NULL", column, *got[i])
		}
	}
}

// Rows that start as Go values and turn to strings are sent as a binary COPY
// and then a text one. The count covers both, and so do the rows.
func TestLiveCopyChangesFormatMidStream(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)
	name := table[1 : len(table)-1]

	rows := [][]any{{1, "one"}, {2, nil}, {"3", "three"}, {4, "four"}, {" 5 ", "five"}}
	copied, err := conn.CopyFrom(ctx, []string{name}, []string{"id", "note"}, CopyFromRows(rows))
	if err != nil || copied != 5 {
		t.Fatalf("CopyFrom = %d, %v", copied, err)
	}
	if conn.TxStatus() != wire.TxIdle {
		t.Fatalf("status = %c after a COPY outside a transaction, want idle", conn.TxStatus())
	}
	var sum int
	if err := conn.QueryRow(ctx, "SELECT sum(id) FROM "+table).Scan(&sum); err != nil || sum != 15 {
		t.Fatalf("sum(id) = %d, %v, want 15", sum, err)
	}
}

// The two COPYs of a stream that changed format are one unit of work. A row the
// text part cannot take must take the binary part's rows down with it, or a
// failed CopyFrom would leave half its rows behind.
func TestLiveCopyFailureAfterChangingFormatCopiesNothing(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)
	name := table[1 : len(table)-1]

	rows := [][]any{{1, "one"}, {2, "two"}, {"3", "three"}, {"1", "again"}}
	copied, err := conn.CopyFrom(ctx, []string{name}, []string{"id", "note"}, CopyFromRows(rows))
	var failure *wire.Error
	if !errors.As(err, &failure) || failure.Code != "23505" || copied != 0 {
		t.Fatalf("CopyFrom = %d, %v, want a unique violation", copied, err)
	}
	if n := countRows(t, conn, table); n != 0 {
		t.Fatalf("%d rows survived a failed CopyFrom", n)
	}
}

// A table with a column the driver has no binary encoding for is copied in
// text, where the server parses every value.
func TestLiveCopyTextForOtherTypes(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := copyTable(t, conn, "id int, amount numeric(10,2), tags text[], at time")
	rows := [][]any{{1, "12.50", []string{"a", "b c"}, "13:14:15"}, {2, 3, nil, nil}}
	copied, err := conn.CopyFrom(ctx, []string{table}, []string{"id", "amount", "tags", "at"}, CopyFromRows(rows))
	if err != nil || copied != 2 {
		t.Fatalf("CopyFrom = %d, %v", copied, err)
	}
	var total, tags, at string
	query := "SELECT sum(amount)::text, max(tags[2]), max(at)::text FROM " + QuoteIdentifier(table)
	if err := conn.QueryRow(ctx, query).Scan(&total, &tags, &at); err != nil || total != "15.50" || tags != "b c" || at != "13:14:15" {
		t.Fatalf("read back %q, %q, %q, %v", total, tags, at, err)
	}
}

// failingSource yields rows until it has given good of them, then fails.
type failingSource struct {
	good, given int
	err         error
}

func (f *failingSource) Next() bool { f.given++; return f.given <= f.good }

func (f *failingSource) Values() ([]any, error) { return []any{f.given, "row"}, nil }

func (f *failingSource) Err() error { return f.err }

// A source that fails must leave nothing copied and the connection ready for
// the next query, whether the failure comes before the COPY command has been
// sent, which it is with the first chunk of rows, or well after.
func TestLiveCopySourceFailure(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)
	name := table[1 : len(table)-1]
	broken := errors.New("source broke")

	for _, good := range []int{0, 3, 50000} {
		copied, err := conn.CopyFrom(ctx, []string{name}, []string{"id", "note"}, &failingSource{good: good, err: broken})
		if !errors.Is(err, broken) || copied != 0 {
			t.Fatalf("after %d rows: CopyFrom = %d, %v, want the source's error", good, copied, err)
		}
		if conn.TxStatus() != wire.TxIdle || conn.Busy() {
			t.Fatalf("after %d rows: connection left in status %c, busy %v", good, conn.TxStatus(), conn.Busy())
		}
		if n := countRows(t, conn, table); n != 0 {
			t.Fatalf("after %d rows: %d rows survived", good, n)
		}
	}

	_, err := conn.CopyFrom(ctx, []string{name}, []string{"id", "note"}, CopyFromRows([][]any{{1, "a"}, {make(chan int), "b"}}))
	var argument *ArgumentError
	if !errors.As(err, &argument) || argument.Index != 0 {
		t.Fatalf("CopyFrom with an unencodable value = %v, want an ArgumentError", err)
	}
	if n := countRows(t, conn, table); n != 0 {
		t.Fatalf("%d rows survived an unencodable value", n)
	}
}

// A COPY the server refuses, before or after it has taken rows, reports the
// server's error and leaves the connection usable.
func TestLiveCopyServerFailure(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)
	name := table[1 : len(table)-1]
	rows := CopyFromRows([][]any{{1, "a"}})

	cases := []struct {
		table   string
		columns []string
		code    string
	}{
		{"caduceus_no_such_table", []string{"id"}, "42P01"},
		{name, []string{"id", "missing"}, "42703"},
	}
	for _, c := range cases {
		_, err := conn.CopyFrom(ctx, []string{c.table}, c.columns, rows)
		var failure *wire.Error
		if !errors.As(err, &failure) || failure.Code != c.code {
			t.Fatalf("CopyFrom(%s %v) = %v, want %s", c.table, c.columns, err, c.code)
		}
		if conn.TxStatus() != wire.TxIdle {
			t.Fatalf("connection left in status %c", conn.TxStatus())
		}
	}

	many := make([][]any, 100000)
	for i := range many {
		many[i] = []any{i % 90000, "row"}
	}
	_, err := conn.CopyFrom(ctx, []string{name}, []string{"id", "note"}, CopyFromRows(many))
	var failure *wire.Error
	if !errors.As(err, &failure) || failure.Code != "23505" {
		t.Fatalf("CopyFrom with a duplicate key = %v, want 23505", err)
	}
	if n := countRows(t, conn, table); n != 0 {
		t.Fatalf("%d rows survived a failed CopyFrom", n)
	}
}

// Inside a transaction a COPY is part of it: rolled back with it, and a failed
// one leaves the transaction failed rather than ended.
func TestLiveCopyInATransaction(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)
	name := table[1 : len(table)-1]

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	many := make([][]any, 150000)
	for i := range many {
		many[i] = []any{i, "row"}
	}
	if copied, err := tx.CopyFrom(ctx, []string{name}, []string{"id", "note"}, CopyFromRows(many)); err != nil || copied != 150000 {
		t.Fatalf("CopyFrom = %d, %v", copied, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, conn, table); n != 0 {
		t.Fatalf("%d rows survived the rollback", n)
	}

	tx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.CopyFrom(ctx, []string{name}, []string{"id", "note"}, CopyFromRows([][]any{{1, "a"}, {1, "b"}})); err == nil {
		t.Fatal("CopyFrom with a duplicate key succeeded")
	}
	if conn.TxStatus() != wire.TxFailed {
		t.Fatalf("status = %c after a failed COPY in a transaction, want failed", conn.TxStatus())
	}
	// A COPY ends only a transaction it began. In the caller's failed one it is
	// refused like any statement, and the transaction is left for the caller.
	_, err = tx.CopyFrom(ctx, []string{name}, []string{"id", "note"}, CopyFromRows([][]any{{2, "c"}}))
	var failure *wire.Error
	if !errors.As(err, &failure) || failure.Code != "25P02" || conn.TxStatus() != wire.TxFailed {
		t.Fatalf("CopyFrom in a failed transaction = %v, status %c", err, conn.TxStatus())
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

// smallSendBuffer shrinks the connection's send buffer to a few kilobytes, so
// that what the client writes has to be taken by the server rather than sit in
// the client's own kernel: the path a client that writes without reading
// depends on.
func smallSendBuffer(t *testing.T, conn *Conn) {
	t.Helper()
	tcp, ok := conn.Wire().NetConn().(*net.TCPConn)
	if !ok {
		t.Skip("not a TCP connection")
	}
	if err := tcp.SetWriteBuffer(4 << 10); err != nil {
		t.Fatal(err)
	}
}

// kilobyte is an argument large enough that a few thousand statements carrying
// it, and their results, are more than any socket buffer holds.
var kilobyte = strings.Repeat("x", 1000)

// A batch used to be written in one piece before anything was read. The server
// answers as it goes, so once its replies to a large batch had filled the path
// back it stopped reading, the client's write could not finish, and each side
// waited on the other until the context gave up.
func TestLiveBatchLargerThanTheSocketBuffers(t *testing.T) {
	for _, options := range []Options{{}, {StatementCacheCapacity: -1}} {
		conn := liveConn(t, options)
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()

		const statements = 30000
		queries := make([]Query, statements)
		for i := range queries {
			queries[i] = Query{SQL: "SELECT $1::int8, $2::text", Arguments: []any{int64(i), kilobyte}}
		}
		results, err := conn.SendBatch(ctx, queries)
		if err != nil {
			t.Fatalf("SendBatch = %v", err)
		}
		for i := range statements {
			var n int64
			var filler string
			if err := results.QueryRow().Scan(&n, &filler); err != nil || n != int64(i) || filler != kilobyte {
				t.Fatalf("statement %d = %d, %d bytes, %v", i, n, len(filler), err)
			}
		}
		if err := results.Close(); err != nil {
			t.Fatalf("Close = %v", err)
		}
		if conn.Busy() || conn.TxStatus() != wire.TxIdle {
			t.Fatalf("connection left busy %v, status %c", conn.Busy(), conn.TxStatus())
		}
	}
}

// A batch closed without its results being read still runs all of it, however
// many windows it goes in.
func TestLiveLargeBatchClosedUnread(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)

	const statements = 5000
	queries := make([]Query, statements)
	for i := range queries {
		queries[i] = Query{SQL: "INSERT INTO " + table + " VALUES ($1, $2)", Arguments: []any{i, kilobyte}}
	}
	results, err := conn.SendBatch(ctx, queries)
	if err != nil {
		t.Fatal(err)
	}
	if tag, err := results.Exec(); err != nil || tag != "INSERT 0 1" {
		t.Fatalf("first statement = %q, %v", tag, err)
	}
	if err := results.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if n := countRows(t, conn, table); n != statements {
		t.Fatalf("%d rows inserted, want %d", n, statements)
	}
}

// A batch is one transaction however it is sent. A statement that fails in an
// early window must undo the statements before it and stop the windows not yet
// sent from running, and every statement after it reports the failure: the
// one right behind it used to report success with an empty tag, though the
// server never ran it.
func TestLiveBatchFailure(t *testing.T) {
	for _, statements := range []int{4, 5000} {
		conn := liveConn(t, Options{StatementCacheCapacity: -1})
		ctx := t.Context()
		table := liveTable(t, conn)

		queries := make([]Query, statements)
		for i := range queries {
			queries[i] = Query{SQL: "INSERT INTO " + table + " VALUES ($1, $2)", Arguments: []any{i, kilobyte}}
		}
		queries[2].Arguments = []any{0, "a duplicate key"}
		results, err := conn.SendBatch(ctx, queries)
		if err != nil {
			t.Fatal(err)
		}
		for i := range statements {
			_, err := results.Exec()
			var failure *wire.Error
			if failed := errors.As(err, &failure) && failure.Code == "23505"; failed != (i >= 2) {
				t.Fatalf("%d statements: statement %d = %v", statements, i, err)
			}
		}
		if _, err := results.Exec(); err == nil {
			t.Fatal("a result past the end of the batch was reported")
		}
		if err := results.Close(); err == nil {
			t.Fatal("Close did not report the failure")
		}
		if conn.Busy() || conn.TxStatus() != wire.TxIdle {
			t.Fatalf("connection left busy %v, status %c", conn.Busy(), conn.TxStatus())
		}
		if n := countRows(t, conn, table); n != 0 {
			t.Fatalf("%d statements: %d rows survived a failed batch", statements, n)
		}
	}
}

// An argument that cannot be encoded fails the batch before any of it is sent,
// wherever in the batch it is.
func TestLiveLargeBatchWithABadArgument(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)

	queries := make([]Query, 5000)
	for i := range queries {
		queries[i] = Query{SQL: "INSERT INTO " + table + " VALUES ($1, $2)", Arguments: []any{i, kilobyte}}
	}
	queries[4999].Arguments = []any{make(chan int), "unencodable"}
	_, err := conn.SendBatch(ctx, queries)
	var argument *ArgumentError
	if !errors.As(err, &argument) {
		t.Fatalf("SendBatch = %v, want an ArgumentError", err)
	}
	if n := countRows(t, conn, table); n != 0 {
		t.Fatalf("%d rows inserted by a batch that was never sent", n)
	}
}

// Thousands of different statements are described a window at a time too.
func TestLiveBatchOfDistinctStatements(t *testing.T) {
	for _, options := range []Options{{}, {StatementCacheCapacity: -1}} {
		conn := liveConn(t, options)
		ctx := t.Context()
		queries := make([]Query, 3000)
		for i := range queries {
			queries[i] = Query{SQL: "SELECT " + strconv.Itoa(i) + " + $1::int", Arguments: []any{1}}
		}
		results, err := conn.SendBatch(ctx, queries)
		if err != nil {
			t.Fatal(err)
		}
		for i := range queries {
			var n int
			if err := results.QueryRow().Scan(&n); err != nil || n != i+1 {
				t.Fatalf("statement %d = %d, %v", i, n, err)
			}
		}
		if err := results.Close(); err != nil {
			t.Fatal(err)
		}
		var one int
		if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
			t.Fatalf("query after the batch = %d, %v", one, err)
		}
	}
}

// The same for a pipeline, outside a transaction and inside one, with results
// far larger than the statements that ask for them.
func TestLivePipelineLargerThanTheSocketBuffers(t *testing.T) {
	for _, inTransaction := range []bool{false, true} {
		conn := liveConn(t, Options{})
		smallSendBuffer(t, conn)
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()

		const statements = 20000
		queries := make([]Query, statements)
		for i := range queries {
			queries[i] = Query{SQL: "SELECT $1::int8, repeat('x', 2000)", Arguments: []any{int64(i)}}
		}
		send := conn.SendPipeline
		var tx *Tx
		if inTransaction {
			var err error
			if tx, err = conn.Begin(ctx); err != nil {
				t.Fatal(err)
			}
			send = tx.SendPipeline
		}
		results, err := send(ctx, queries)
		if err != nil {
			t.Fatalf("SendPipeline = %v", err)
		}
		for i := range statements {
			var n int64
			var filler string
			if err := results.QueryRow().Scan(&n, &filler); err != nil || n != int64(i) || len(filler) != 2000 {
				t.Fatalf("in transaction %v: statement %d = %d, %d bytes, %v", inTransaction, i, n, len(filler), err)
			}
		}
		if err := results.Close(); err != nil {
			t.Fatalf("Close = %v", err)
		}
		if tx != nil {
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
}
