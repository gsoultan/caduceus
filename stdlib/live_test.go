// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/gsoultan/caduceus"
	"github.com/gsoultan/caduceus/stdlib"
	"github.com/gsoultan/caduceus/wire"
)

// Tests against a real server, skipped unless DATABASE_URL is set.
func liveDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := sql.Open(stdlib.DriverName, dsn)
	if err != nil {
		t.Fatalf("sql.Open() = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// liveTable creates a table for one test, dropped after it.
func liveTable(t *testing.T, db *sql.DB, columns string) string {
	t.Helper()
	name := fmt.Sprintf("stdlib_%d", time.Now().UnixNano())
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE "+name+" ("+columns+")"); err != nil {
		t.Fatalf("CREATE TABLE = %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE "+name) })
	return name
}

func TestLiveColumnTypes(t *testing.T) {
	db := liveDB(t)
	rows, err := db.QueryContext(t.Context(), `SELECT 1::int2, 2::int4, 3::int8, 1.5::float4, 2.25::float8,
		true, '\x0102'::bytea, '2026-01-02'::date, '2026-01-02 03:04:05+00'::timestamptz,
		12.50::numeric(10,2), 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'::uuid, '1 day'::interval,
		'{"a": 1}'::jsonb, ARRAY[1, 2], NULL::text, 'infinity'::timestamp, 'x'::varchar(20)`)
	if err != nil {
		t.Fatalf("Query() = %v", err)
	}
	defer rows.Close()
	want := []any{
		int64(1), int64(2), int64(3), float64(1.5), 2.25,
		true, []byte{1, 2}, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		"12.50", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "1 day",
		`{"a": 1}`, "{1,2}", nil, "infinity", "x",
	}
	values := make([]any, len(want))
	dest := make([]any, len(want))
	for i := range values {
		dest[i] = &values[i]
	}
	if !rows.Next() {
		t.Fatalf("Next() = false, %v", rows.Err())
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatalf("Scan() = %v", err)
	}
	for i := range want {
		if !sameValue(values[i], want[i]) {
			t.Errorf("column %d = %#v, want %#v", i+1, values[i], want[i])
		}
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("ColumnTypes() = %v", err)
	}
	if got := types[1].DatabaseTypeName(); got != "INT4" {
		t.Errorf("DatabaseTypeName() = %q, want INT4", got)
	}
	if got := types[13].DatabaseTypeName(); got != "_INT4" {
		t.Errorf("DatabaseTypeName() = %q, want _INT4", got)
	}
	if got := types[8].ScanType(); got != reflect.TypeFor[time.Time]() {
		t.Errorf("ScanType() = %v, want time.Time", got)
	}
	if p, s, ok := types[9].DecimalSize(); p != 10 || s != 2 || !ok {
		t.Errorf("DecimalSize() = %d, %d, %v; want 10, 2, true", p, s, ok)
	}
	if n, ok := types[16].Length(); n != 20 || !ok {
		t.Errorf("Length() = %d, %v; want 20, true", n, ok)
	}
}

func sameValue(got, want any) bool {
	switch w := want.(type) {
	case []byte:
		g, ok := got.([]byte)
		return ok && string(g) == string(w)
	case time.Time:
		g, ok := got.(time.Time)
		return ok && g.Equal(w)
	}
	return got == want
}

// celsius is a driver.Valuer, which caduceus calls itself.
type celsius float64

func (c celsius) Value() (driver.Value, error) { return fmt.Sprintf("%.1f", float64(c)), nil }

func TestLiveArguments(t *testing.T) {
	db := liveDB(t)
	ctx := t.Context()
	var text string
	if err := db.QueryRowContext(ctx, "SELECT $1::text", celsius(21.5)).Scan(&text); err != nil || text != "21.5" {
		t.Errorf("a Valuer = %q, %v; want 21.5", text, err)
	}
	var null sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT $1::text", (*celsius)(nil)).Scan(&null); err != nil || null.Valid {
		t.Errorf("a nil *Valuer = %+v, %v; want NULL", null, err)
	}
	// database/sql's own conversion refuses a slice; caduceus binds it as an
	// array.
	var sum int64
	if err := db.QueryRowContext(ctx, "SELECT sum(x) FROM unnest($1::int[]) x", []int32{1, 2, 3}).Scan(&sum); err != nil || sum != 6 {
		t.Errorf("an array argument = %d, %v; want 6", sum, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT @a::int + @b::int", caduceus.NamedArgs{"a": 2, "b": 3}).Scan(&sum); err != nil || sum != 5 {
		t.Errorf("caduceus.NamedArgs = %d, %v; want 5", sum, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT $1::int", sql.Named("a", 1)).Scan(&sum); !errors.Is(err, stdlib.ErrNamedArg) {
		t.Errorf("sql.Named error = %v, want ErrNamedArg", err)
	}
	// Bound, not interpolated: the quote is part of the value.
	if err := db.QueryRowContext(ctx, "SELECT $1::text", "it's; DROP TABLE x").Scan(&text); err != nil || text != "it's; DROP TABLE x" {
		t.Errorf("a quoted string = %q, %v", text, err)
	}
}

func TestLiveExecAndPrepare(t *testing.T) {
	db := liveDB(t)
	ctx := t.Context()
	table := liveTable(t, db, "id int PRIMARY KEY, name text")
	insert, err := db.PrepareContext(ctx, "INSERT INTO "+table+" VALUES ($1, $2)")
	if err != nil {
		t.Fatalf("Prepare() = %v", err)
	}
	for i := range 5 {
		if _, err := insert.ExecContext(ctx, i, fmt.Sprint("n", i)); err != nil {
			t.Fatalf("Stmt.Exec(%d) = %v", i, err)
		}
	}
	if err := insert.Close(); err != nil {
		t.Errorf("Stmt.Close() = %v", err)
	}
	res, err := db.ExecContext(ctx, "UPDATE "+table+" SET name = name || '!' WHERE id < $1", 3)
	if err != nil {
		t.Fatalf("Exec() = %v", err)
	}
	if n, err := res.RowsAffected(); n != 3 || err != nil {
		t.Errorf("RowsAffected() = %d, %v; want 3", n, err)
	}
	if _, err := res.LastInsertId(); !errors.Is(err, stdlib.ErrNoLastInsertID) {
		t.Errorf("LastInsertId() error = %v", err)
	}
	// Several statements without arguments go over the simple protocol.
	if _, err := db.ExecContext(ctx, "SELECT 1; SELECT 2"); err != nil {
		t.Errorf("Exec() of two statements = %v", err)
	}
	query, err := db.PrepareContext(ctx, "SELECT name FROM "+table+" WHERE id = $1")
	if err != nil {
		t.Fatalf("Prepare() = %v", err)
	}
	defer query.Close()
	var name string
	if err := query.QueryRowContext(ctx, 1).Scan(&name); err != nil || name != "n1!" {
		t.Errorf("Stmt.QueryRow(1) = %q, %v; want n1!", name, err)
	}
	if err := query.QueryRowContext(ctx, 99).Scan(&name); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Stmt.QueryRow(99) error = %v, want sql.ErrNoRows", err)
	}
	var none string
	if _, err := db.ExecContext(ctx, "SELECT * FROM no_such_table_stdlib"); err == nil {
		t.Error("Exec() of a missing table = nil error")
	}
	if err := db.QueryRowContext(ctx, "SELECT 'still usable'").Scan(&none); err != nil {
		t.Errorf("a query after an error = %v", err)
	}
}

// A statement closed on its connection is deallocated there, so a program
// preparing in a loop does not fill the server's memory with statements.
func TestLiveStmtCloseDeallocates(t *testing.T) {
	db := liveDB(t)
	ctx := t.Context()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn() = %v", err)
	}
	defer conn.Close()
	count := func() int {
		var n int
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM pg_prepared_statements WHERE name LIKE 'stdlib\_%'`).Scan(&n); err != nil {
			t.Fatalf("counting statements = %v", err)
		}
		return n
	}
	before := count()
	stmt, err := conn.PrepareContext(ctx, "SELECT $1::int")
	if err != nil {
		t.Fatalf("Prepare() = %v", err)
	}
	if got := count(); got != before+1 {
		t.Errorf("prepared statements = %d, want %d", got, before+1)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("Stmt.Close() = %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Errorf("second Stmt.Close() = %v", err)
	}
	if got := count(); got != before {
		t.Errorf("prepared statements after Close = %d, want %d", got, before)
	}
}

func TestLiveTransactions(t *testing.T) {
	db := liveDB(t)
	ctx := t.Context()
	table := liveTable(t, db, "id int")
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx() = %v", err)
	}
	var level, readOnly string
	if err := tx.QueryRowContext(ctx, "SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only')").Scan(&level, &readOnly); err != nil {
		t.Fatalf("reading the settings = %v", err)
	}
	if level != "serializable" || readOnly != "on" {
		t.Errorf("transaction = %s, read only %s; want serializable, on", level, readOnly)
	}
	if err := tx.Rollback(); err != nil {
		t.Errorf("Rollback() = %v", err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() = %v", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" VALUES (1)"); err != nil {
		t.Fatalf("Exec() = %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() = %v", err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() = %v", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" VALUES (2)"); err != nil {
		t.Fatalf("Exec() = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() = %v", err)
	}
	var ids []int64
	rows, err := db.QueryContext(ctx, "SELECT id FROM "+table)
	if err != nil {
		t.Fatalf("Query() = %v", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("Scan() = %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil || len(ids) != 1 || ids[0] != 2 {
		t.Errorf("rows = %v, %v; want only the committed 2", ids, err)
	}
	if _, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSnapshot}); !errors.Is(err, stdlib.ErrIsolationLevel) {
		t.Errorf("BeginTx(LevelSnapshot) error = %v, want ErrIsolationLevel", err)
	}
}

// A transaction whose context ends is rolled back by database/sql, and the
// connection serves the next caller with nothing left open.
func TestLiveCancelledTransaction(t *testing.T) {
	db := liveDB(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(t.Context())
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() = %v", err)
	}
	if _, err := tx.ExecContext(ctx, "SET LOCAL application_name = 'cancelled'"); err != nil {
		t.Fatalf("Exec() = %v", err)
	}
	cancel()
	if err := tx.Commit(); !errors.Is(err, sql.ErrTxDone) && !errors.Is(err, context.Canceled) {
		t.Errorf("Commit() after cancel = %v", err)
	}
	var name string
	if err := db.QueryRowContext(t.Context(), "SELECT current_setting('application_name')").Scan(&name); err != nil || name == "cancelled" {
		t.Errorf("application_name = %q, %v; the transaction was left open", name, err)
	}
}

func TestLiveQueryContextTimeout(t *testing.T) {
	db := liveDB(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := db.ExecContext(ctx, "SELECT pg_sleep(10)"); err == nil {
		t.Fatal("Exec() past its deadline = nil error")
	}
	var one int
	if err := db.QueryRowContext(t.Context(), "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Errorf("a query after a timeout = %d, %v", one, err)
	}
}

// A connection the server ended while it sat idle in the pool is found dead
// before it is handed out, and the caller gets a fresh one rather than the
// error: ResetSession checks it.
func TestLiveIdleKilledConnectionIsReplaced(t *testing.T) {
	db := liveDB(t)
	db.SetMaxOpenConns(1)
	ctx := t.Context()
	var pid int
	if err := db.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatalf("pg_backend_pid() = %v", err)
	}
	terminate(t, pid)
	var after int
	if err := db.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&after); err != nil {
		t.Fatalf("the first query after the kill = %v, want it to run on a fresh connection", err)
	}
	if after == pid {
		t.Errorf("backend pid = %d after it was terminated", after)
	}
}

// A connection found dead says driver.ErrBadConn on every path database/sql
// asks it, so that the pool discards it and retries elsewhere rather than
// handing it out again.
func TestLiveDeadConnReportsBadConn(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	connector, err := stdlib.Driver{}.OpenConnector(dsn)
	if err != nil {
		t.Fatalf("OpenConnector() = %v", err)
	}
	ctx := t.Context()
	opened, err := connector.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	conn := opened.(*stdlib.Conn)
	defer conn.Close()
	var pid int
	if err := conn.Conn().QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatalf("pg_backend_pid() = %v", err)
	}
	terminate(t, pid)
	if err := conn.ResetSession(ctx); !errors.Is(err, driver.ErrBadConn) {
		t.Errorf("ResetSession() = %v, want driver.ErrBadConn", err)
	}
	// What CheckConn saw, a later statement fails on; after it the
	// connection knows it is closed.
	_, _ = conn.ExecContext(ctx, "SELECT 1", nil)
	if conn.IsValid() {
		t.Error("IsValid() = true for a dead connection")
	}
	if _, err := conn.QueryContext(ctx, "SELECT 1", nil); !errors.Is(err, driver.ErrBadConn) {
		t.Errorf("QueryContext() = %v, want driver.ErrBadConn", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT 1", nil); !errors.Is(err, driver.ErrBadConn) {
		t.Errorf("ExecContext() = %v, want driver.ErrBadConn", err)
	}
	if _, err := conn.PrepareContext(ctx, "SELECT 1"); !errors.Is(err, driver.ErrBadConn) {
		t.Errorf("PrepareContext() = %v, want driver.ErrBadConn", err)
	}
	if _, err := conn.BeginTx(ctx, driver.TxOptions{}); !errors.Is(err, driver.ErrBadConn) {
		t.Errorf("BeginTx() = %v, want driver.ErrBadConn", err)
	}
	if err := conn.Ping(ctx); !errors.Is(err, driver.ErrBadConn) {
		t.Errorf("Ping() = %v, want driver.ErrBadConn", err)
	}
	if err := conn.Close(); err != nil {
		t.Errorf("Close() of a dead connection = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Errorf("second Close() = %v", err)
	}
}

// terminate ends a backend from another connection and waits until it is
// gone, so the socket has been closed by the time it returns.
func terminate(t *testing.T, pid int) {
	t.Helper()
	killer := liveDB(t)
	ctx := t.Context()
	if _, err := killer.ExecContext(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatalf("pg_terminate_backend() = %v", err)
	}
	for range 200 {
		var alive bool
		if err := killer.QueryRowContext(ctx, "SELECT EXISTS (SELECT FROM pg_stat_activity WHERE pid = $1)", pid).Scan(&alive); err != nil {
			t.Fatalf("pg_stat_activity = %v", err)
		}
		if !alive {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("backend %d still alive after pg_terminate_backend", pid)
}

func TestLiveRawConn(t *testing.T) {
	db := liveDB(t)
	ctx := t.Context()
	table := liveTable(t, db, "id int, name text")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn() = %v", err)
	}
	defer conn.Close()
	err = conn.Raw(func(driverConn any) error {
		raw := driverConn.(*stdlib.Conn).Conn()
		_, err := raw.CopyFrom(ctx, []string{table}, []string{"id", "name"}, caduceus.CopyFromRows([][]any{{1, "a"}, {2, "b"}}))
		return err
	})
	if err != nil {
		t.Fatalf("CopyFrom through Raw = %v", err)
	}
	var n int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 2 {
		t.Errorf("count = %d, %v; want 2", n, err)
	}
}

func TestLiveOpenDB(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := wire.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig() = %v", err)
	}
	config.RuntimeParams["application_name"] = "stdlib-test"
	db := stdlib.OpenDB(config, caduceus.Options{})
	defer db.Close()
	// The connector copied the config: a change now does not reach it.
	config.RuntimeParams["application_name"] = "changed"
	var name string
	if err := db.QueryRowContext(t.Context(), "SELECT current_setting('application_name')").Scan(&name); err != nil || name != "stdlib-test" {
		t.Errorf("application_name = %q, %v; want stdlib-test", name, err)
	}
	if err := db.PingContext(t.Context()); err != nil {
		t.Errorf("Ping() = %v", err)
	}
}
