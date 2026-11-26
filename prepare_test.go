// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// prepareOptions are the ways a connection runs the statements it prepares
// itself, kept on the server or not: a statement the caller prepares is kept
// there under every one of them.
var prepareOptions = append([]Options{{}}, notKept...)

// preparedOnServer counts the statements of the session the server holds as
// name.
func preparedOnServer(t *testing.T, conn *Conn, name string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(t.Context(), "SELECT count(*)::int4 FROM pg_prepared_statements WHERE name = $1", name).Scan(&n); err != nil {
		t.Fatalf("pg_prepared_statements = %v", err)
	}
	return n
}

// TestLivePrepareDescribes proves Prepare returns what the server said of the
// statement, under the name given, and that the description is a copy: what a
// caller does to it is not what the next Prepare of the name returns.
func TestLivePrepareDescribes(t *testing.T) {
	for _, options := range prepareOptions {
		conn := liveConn(t, options)
		ctx := t.Context()
		const sql = "SELECT $1::int4 AS n, $2::text AS t"
		sd, err := conn.Prepare(ctx, "described", sql)
		if err != nil {
			t.Fatalf("%+v: Prepare() = %v", options, err)
		}
		if sd.Name != "described" || sd.SQL != sql {
			t.Fatalf("%+v: Name, SQL = %q, %q", options, sd.Name, sd.SQL)
		}
		if len(sd.ParamOIDs) != 2 || sd.ParamOIDs[0] != 23 || sd.ParamOIDs[1] != 25 {
			t.Fatalf("%+v: ParamOIDs = %v, want [23 25]", options, sd.ParamOIDs)
		}
		if len(sd.Fields) != 2 || sd.Fields[0].Name != "n" || sd.Fields[1].DataTypeOID != 25 {
			t.Fatalf("%+v: Fields = %+v", options, sd.Fields)
		}
		sd.ParamOIDs[0], sd.Fields[0].Name = 0, "scribbled"
		again, err := conn.Prepare(ctx, "described", sql)
		if err != nil || again.ParamOIDs[0] != 23 || again.Fields[0].Name != "n" {
			t.Fatalf("%+v: the description aliases the connection's: %+v, %v", options, again, err)
		}
		if n := preparedOnServer(t, conn, "described"); n != 1 {
			t.Fatalf("%+v: the server holds %d statements named described, want 1", options, n)
		}
	}
}

// TestLivePrepareAgain proves preparing a name again with the same text costs
// no round trip and returns what is held, and that with another text the
// server refuses the Parse with 42P05, as in pgx, and the statement held under
// the name stays and runs by it. It used to be closed and replaced on its own,
// so code that reused a name by mistake swapped what other code ran by it.
func TestLivePrepareAgain(t *testing.T) {
	for _, options := range prepareOptions {
		conn, trips := countedConn(t, options)
		ctx := t.Context()
		if _, err := conn.Prepare(ctx, "again", "SELECT 1::int4"); err != nil {
			t.Fatalf("%+v: Prepare() = %v", options, err)
		}
		trips()
		if _, err := conn.Prepare(ctx, "again", "SELECT 1::int4"); err != nil {
			t.Fatalf("%+v: Prepare() again = %v", options, err)
		}
		if n := trips(); n != 0 {
			t.Fatalf("%+v: preparing the same text again took %d round trips, want 0", options, n)
		}
		if _, err := conn.Prepare(ctx, "again", "SELECT 'two'::text"); sqlState(err) != "42P05" {
			t.Fatalf("%+v: Prepare() with another text = %v, want 42P05", options, err)
		}
		var got int32
		if err := conn.QueryRow(ctx, "again").Scan(&got); err != nil || got != 1 {
			t.Fatalf("%+v: run by name = %d, %v; want the first text's result", options, got, err)
		}
		if held := conn.prepared["again"]; held == nil || held.sql != "SELECT 1::int4" {
			t.Fatalf("%+v: the statement held under the name was replaced", options)
		}
		if n := preparedOnServer(t, conn, "again"); n != 1 {
			t.Fatalf("%+v: the server holds %d statements named again, want 1", options, n)
		}
		if err := conn.Deallocate(ctx, "again"); err != nil {
			t.Fatalf("%+v: Deallocate() = %v", options, err)
		}
		sd, err := conn.Prepare(ctx, "again", "SELECT 'two'::text")
		if err != nil || sd.Fields[0].DataTypeOID != 25 {
			t.Fatalf("%+v: Prepare() after Deallocate = %+v, %v", options, sd, err)
		}
		var text string
		if err := conn.QueryRow(ctx, "again").Scan(&text); err != nil || text != "two" {
			t.Fatalf("%+v: run by name after Deallocate = %q, %v; want the new text's result", options, text, err)
		}
	}
}

// TestLiveRunPreparedByName proves every way of running a statement runs the
// prepared one when given its name: a Bind of that statement and no Parse at
// all. Exec without arguments used to send the name over the simple protocol,
// where the server read it as SQL.
func TestLiveRunPreparedByName(t *testing.T) {
	type run func(ctx context.Context, conn *Conn, name string, settle func()) (int, error)
	scanOne := func(row *Row) (int, error) {
		var n int
		err := row.Scan(&n)
		return n, err
	}
	paths := []struct {
		name string
		run  run
	}{
		{"Exec", func(ctx context.Context, conn *Conn, name string, _ func()) (int, error) {
			tag, err := conn.Exec(ctx, name, 41)
			return int(tag.RowsAffected()) + 41, err
		}},
		{"Query", func(ctx context.Context, conn *Conn, name string, _ func()) (int, error) {
			rows, err := conn.Query(ctx, name, 41)
			if err != nil {
				return 0, err
			}
			defer rows.Close()
			n := 0
			for rows.Next() {
				if err := rows.Scan(&n); err != nil {
					return 0, err
				}
			}
			return n, rows.Err()
		}},
		{"QueryRow", func(ctx context.Context, conn *Conn, name string, _ func()) (int, error) {
			return scanOne(conn.QueryRow(ctx, name, 41))
		}},
		{"QueryInto", func(ctx context.Context, conn *Conn, name string, _ func()) (int, error) {
			var rows Rows
			if err := conn.QueryInto(ctx, &rows, name, 41); err != nil {
				return 0, err
			}
			defer rows.Close()
			n := 0
			for rows.Next() {
				if err := rows.Scan(&n); err != nil {
					return 0, err
				}
			}
			return n, rows.Err()
		}},
		{"SendBatch", func(ctx context.Context, conn *Conn, name string, _ func()) (int, error) {
			results, err := conn.SendBatch(ctx, []Query{{SQL: name, Arguments: []any{41}}, {SQL: name, Arguments: []any{1}}})
			if err != nil {
				return 0, err
			}
			defer results.Close()
			n, err := scanOne(results.QueryRow())
			if second, err2 := scanOne(results.QueryRow()); err == nil && (err2 != nil || second != 2) {
				return 0, errors.Join(err2, errors.New("the second statement of the batch"))
			}
			return n, err
		}},
		{"SendPipeline", func(ctx context.Context, conn *Conn, name string, _ func()) (int, error) {
			results, err := conn.SendPipeline(ctx, []Query{{SQL: name, Arguments: []any{41}}})
			if err != nil {
				return 0, err
			}
			defer results.Close()
			return scanOne(results.QueryRow())
		}},
		{"Tx.QueryRow", func(ctx context.Context, conn *Conn, name string, settle func()) (int, error) {
			tx, err := conn.Begin(ctx)
			if err != nil {
				return 0, err
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SELECT 1"); err != nil {
				return 0, err
			}
			settle()
			return scanOne(tx.QueryRow(ctx, name, 41))
		}},
		{"Tx.SendPipeline", func(ctx context.Context, conn *Conn, name string, settle func()) (int, error) {
			tx, err := conn.Begin(ctx)
			if err != nil {
				return 0, err
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SELECT 1"); err != nil {
				return 0, err
			}
			settle()
			results, err := tx.SendPipeline(ctx, []Query{{SQL: name, Arguments: []any{41}}})
			if err != nil {
				return 0, err
			}
			defer results.Close()
			return scanOne(results.QueryRow())
		}},
	}
	for _, options := range prepareOptions {
		for _, path := range paths {
			conn, sent := watchedConn(t, options)
			ctx := t.Context()
			if _, err := conn.Prepare(ctx, "plus_one", "SELECT $1::int4 + 1"); err != nil {
				t.Fatalf("%+v %s: Prepare() = %v", options, path.name, err)
			}
			sent()
			n, err := path.run(ctx, conn, "plus_one", func() { sent() })
			if err != nil || n != 42 {
				t.Fatalf("%+v %s: run by name = %d, %v; want 42", options, path.name, n, err)
			}
			bound := false
			for _, message := range sent() {
				if message.kind == 'P' {
					t.Fatalf("%+v %s: a Parse of %q was sent for a prepared statement", options, path.name, message.statement)
				}
				bound = bound || (message.kind == 'B' && message.statement == "plus_one")
			}
			if !bound {
				t.Fatalf("%+v %s: no Bind of the prepared statement was sent", options, path.name)
			}
			usable(t, conn)
		}
	}
}

// TestLiveExecPreparedWithoutArguments proves a prepared statement that takes
// no arguments is run by Exec, which runs everything else without arguments
// over the simple protocol: there its name went to the server as SQL.
func TestLiveExecPreparedWithoutArguments(t *testing.T) {
	for _, options := range prepareOptions {
		conn := liveConn(t, options)
		ctx := t.Context()
		table := liveTable(t, conn)
		if _, err := conn.Prepare(ctx, "fill", "INSERT INTO "+table+" VALUES (1, 'one'), (2, 'two')"); err != nil {
			t.Fatalf("%+v: Prepare() = %v", options, err)
		}
		tag, err := conn.Exec(ctx, "fill")
		if err != nil || tag.RowsAffected() != 2 {
			t.Fatalf("%+v: Exec(name) = %q, %v; want INSERT 0 2", options, tag, err)
		}
	}
}

// TestLiveDeallocate proves a deallocated statement is gone on the server and
// here: its name is SQL again, which the server refuses, and the connection
// stays usable. Deallocating it again, or a name never prepared, is no error.
func TestLiveDeallocate(t *testing.T) {
	for _, options := range prepareOptions {
		conn := liveConn(t, options)
		ctx := t.Context()
		if _, err := conn.Prepare(ctx, "gone", "SELECT $1::int4"); err != nil {
			t.Fatalf("%+v: Prepare() = %v", options, err)
		}
		if err := conn.Deallocate(ctx, "gone"); err != nil {
			t.Fatalf("%+v: Deallocate() = %v", options, err)
		}
		if n := preparedOnServer(t, conn, "gone"); n != 0 {
			t.Fatalf("%+v: the server still holds the statement", options)
		}
		var n int
		if err := conn.QueryRow(ctx, "gone", 1).Scan(&n); sqlState(err) != "42601" {
			t.Fatalf("%+v: run after Deallocate = %v, want the name refused as SQL (42601)", options, err)
		}
		usable(t, conn)
		for _, name := range []string{"gone", "never_prepared", "caduceus_1"} {
			if err := conn.Deallocate(ctx, name); err != nil {
				t.Fatalf("%+v: Deallocate(%q) = %v, want nil", options, name, err)
			}
		}
		// A Deallocate of a name the cache generates must not close the cache's
		// statement of that name.
		if err := conn.QueryRow(ctx, "SELECT $1::int4", 7).Scan(&n); err != nil || n != 7 {
			t.Fatalf("%+v: a cached statement after Deallocate = %d, %v", options, n, err)
		}
	}
}

// TestLiveDeallocateAll proves DeallocateAll closes the caller's statements
// and forgets the cache's: a cached statement run afterwards is parsed again,
// where one still remembered would have failed with 26000.
func TestLiveDeallocateAll(t *testing.T) {
	for _, options := range prepareOptions {
		conn := liveConn(t, options)
		ctx := t.Context()
		var n int
		if err := conn.QueryRow(ctx, "SELECT $1::int4", 1).Scan(&n); err != nil {
			t.Fatalf("%+v: cached statement = %v", options, err)
		}
		for _, name := range []string{"first", "second"} {
			if _, err := conn.Prepare(ctx, name, "SELECT 1"); err != nil {
				t.Fatalf("%+v: Prepare(%q) = %v", options, name, err)
			}
		}
		if err := conn.DeallocateAll(ctx); err != nil {
			t.Fatalf("%+v: DeallocateAll() = %v", options, err)
		}
		if err := conn.QueryRow(ctx, "SELECT $1::int4", 2).Scan(&n); err != nil || n != 2 {
			t.Fatalf("%+v: cached statement after DeallocateAll = %d, %v", options, n, err)
		}
		if got := preparedOnServer(t, conn, "first") + preparedOnServer(t, conn, "second"); got != 0 {
			t.Fatalf("%+v: %d prepared statements left on the server", options, got)
		}
		if len(conn.prepared) != 0 {
			t.Fatalf("%+v: %d prepared statements still held", options, len(conn.prepared))
		}
	}
}

// TestLivePrepareRefused proves a Parse the server refuses returns its error
// and leaves nothing held under a name that was free, and that a refused
// Parse over a held name leaves the held statement prepared and running. That
// one used to be closed ahead of the Parse, and so lost along with it.
func TestLivePrepareRefused(t *testing.T) {
	for _, options := range prepareOptions {
		conn := liveConn(t, options)
		ctx := t.Context()
		if _, err := conn.Prepare(ctx, "broken", "SELEC 1"); sqlState(err) != "42601" {
			t.Fatalf("%+v: Prepare(bad SQL) = %v, want 42601", options, err)
		}
		if conn.prepared["broken"] != nil {
			t.Fatalf("%+v: a statement the server refused is held", options)
		}
		if _, err := conn.Prepare(ctx, "kept", "SELECT 1::int4"); err != nil {
			t.Fatalf("%+v: Prepare() = %v", options, err)
		}
		var refused *wire.Error
		if _, err := conn.Prepare(ctx, "kept", "SELEC 2"); !errors.As(err, &refused) {
			t.Fatalf("%+v: Prepare(bad SQL) over a held name = %v, want a *wire.Error", options, err)
		}
		if conn.prepared["kept"] == nil || preparedOnServer(t, conn, "kept") != 1 {
			t.Fatalf("%+v: the held statement was lost to a refused Parse", options)
		}
		var got int32
		if err := conn.QueryRow(ctx, "kept").Scan(&got); err != nil || got != 1 {
			t.Fatalf("%+v: run by name = %d, %v; want the held statement's result", options, got, err)
		}
		usable(t, conn)
	}
}

// TestLivePreparedAfterAlterTable proves a prepared statement whose table
// changed under it reports the server's 0A000, is not closed by the driver on
// its own, and is parsed afresh by preparing it again. Treated as a statement
// of the cache, it was closed on the server behind the caller's back.
func TestLivePreparedAfterAlterTable(t *testing.T) {
	for _, options := range prepareOptions {
		conn := liveConn(t, options)
		ctx := t.Context()
		table := liveTable(t, conn)
		if _, err := conn.Exec(ctx, "INSERT INTO "+table+" VALUES (1, 'one')"); err != nil {
			t.Fatal(err)
		}
		sql := "SELECT * FROM " + table
		if _, err := conn.Prepare(ctx, "everything", sql); err != nil {
			t.Fatalf("%+v: Prepare() = %v", options, err)
		}
		if _, err := conn.Exec(ctx, "ALTER TABLE "+table+" ADD COLUMN extra int"); err != nil {
			t.Fatal(err)
		}
		var id int
		var note string
		if err := conn.QueryRow(ctx, "everything").Scan(&id, &note); sqlState(err) != "0A000" {
			t.Fatalf("%+v: run after ALTER TABLE = %v, want 0A000", options, err)
		}
		usable(t, conn)
		if n := preparedOnServer(t, conn, "everything"); n != 1 {
			t.Fatalf("%+v: the driver closed the caller's statement", options)
		}
		sd, err := conn.Prepare(ctx, "everything", sql)
		if err != nil || len(sd.Fields) != 3 {
			t.Fatalf("%+v: Prepare() again = %+v, %v; want three columns", options, sd, err)
		}
		var extra *int
		if err := conn.QueryRow(ctx, "everything").Scan(&id, &note, &extra); err != nil || id != 1 || extra != nil {
			t.Fatalf("%+v: run after preparing again = %d %v, %v", options, id, extra, err)
		}
	}
}

// TestLivePrepareInTransaction proves a Prepare inside a transaction sends
// its BEGIN, so the transaction is open on the server, and that the statement
// outlives a rollback, Parse not being transactional. A Prepare that sends
// nothing leaves the BEGIN waiting.
func TestLivePrepareInTransaction(t *testing.T) {
	for _, options := range prepareOptions {
		conn := liveConn(t, options)
		ctx := t.Context()
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Prepare(ctx, "in_tx", "SELECT $1::int4 * 2"); err != nil {
			t.Fatalf("%+v: Tx.Prepare() = %v", options, err)
		}
		if status := conn.Wire().TxStatus(); status != wire.TxActive {
			t.Fatalf("%+v: server status after Tx.Prepare = %q, want 'T': the BEGIN was not sent", options, status)
		}
		var n int
		if err := tx.QueryRow(ctx, "in_tx", 4).Scan(&n); err != nil || n != 8 {
			t.Fatalf("%+v: run in the transaction = %d, %v", options, n, err)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, "in_tx", 5).Scan(&n); err != nil || n != 10 {
			t.Fatalf("%+v: run after the rollback = %d, %v", options, n, err)
		}
		if _, err := tx.Prepare(ctx, "late", "SELECT 1"); !errors.Is(err, ErrTxClosed) {
			t.Fatalf("%+v: Tx.Prepare() after Rollback = %v, want ErrTxClosed", options, err)
		}

		tx, err = conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Prepare(ctx, "in_tx", "SELECT $1::int4 * 2"); err != nil {
			t.Fatal(err)
		}
		if conn.Wire().TxStatus() != wire.TxIdle || conn.TxStatus() != wire.TxActive {
			t.Fatalf("%+v: a Prepare that parsed nothing sent the BEGIN", options)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLivePreparedNeverEvicted proves the statement cache, full and evicting,
// neither closes a statement the caller prepared nor gives one of its own a
// name the caller took: a caller's statement named as the cache names its
// first used to be the one the cache's Parse ran into, with 42P05.
func TestLivePreparedNeverEvicted(t *testing.T) {
	for _, options := range []Options{{StatementCacheCapacity: 2}, notKept[0]} {
		conn, sent := watchedConn(t, options)
		ctx := t.Context()
		if _, err := conn.Prepare(ctx, "caduceus_1", "SELECT 'mine'::text"); err != nil {
			t.Fatalf("%+v: Prepare() = %v", options, err)
		}
		if _, err := conn.Prepare(ctx, "kept", "SELECT $1::int4 + 100"); err != nil {
			t.Fatalf("%+v: Prepare() = %v", options, err)
		}
		for i := range 6 {
			var n int
			sql := "SELECT $1::int4 + " + string(rune('0'+i))
			if err := conn.QueryRow(ctx, sql, 1).Scan(&n); err != nil || n != 1+i {
				t.Fatalf("%+v: cached statement %d = %d, %v", options, i, n, err)
			}
		}
		sent()
		var n int
		if err := conn.QueryRow(ctx, "kept", 1).Scan(&n); err != nil || n != 101 {
			t.Fatalf("%+v: run by name after evictions = %d, %v", options, n, err)
		}
		for _, message := range sent() {
			if message.kind == 'P' {
				t.Fatalf("%+v: the prepared statement was parsed again: the cache evicted it", options)
			}
		}
		var mine string
		if err := conn.QueryRow(ctx, "caduceus_1").Scan(&mine); err != nil || mine != "mine" {
			t.Fatalf("%+v: the caller's caduceus_1 = %q, %v", options, mine, err)
		}
	}
}

// TestLivePrepareNames proves names the server would cut or that are the
// statement's own text still name one statement each: two names that agree
// in their first 63 bytes were one statement on the server, where the second
// Parse failed with 42P05.
func TestLivePrepareNames(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	long := strings.Repeat("n", 63)
	names := []struct {
		name, sql, want string
	}{
		{long + "_a", "SELECT 'a'::text", "a"},
		{long + "_b", "SELECT 'b'::text", "b"},
		{"", "SELECT 'by text'::text", "by text"},
	}
	for _, name := range names {
		sd, err := conn.Prepare(ctx, name.name, name.sql)
		if err != nil {
			t.Fatalf("Prepare(%q) = %v", name.name, err)
		}
		if !strings.HasPrefix(sd.Name, generatedPrefix+"p_") {
			t.Fatalf("Prepare(%q) named it %q on the server", name.name, sd.Name)
		}
	}
	for _, name := range names {
		runBy := name.name
		if runBy == "" {
			runBy = name.sql
		}
		var got string
		if err := conn.QueryRow(ctx, runBy).Scan(&got); err != nil || got != name.want {
			t.Fatalf("run %q = %q, %v; want %q", runBy, got, err, name.want)
		}
	}
}

// TestLivePreparedForgotten proves ForgetStatements, called after DISCARD ALL
// has dropped every statement on the server, forgets the caller's as well:
// held still, its Prepare cost nothing and its run failed with 26000.
func TestLivePreparedForgotten(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	if _, err := conn.Prepare(ctx, "discarded", "SELECT 3"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "DISCARD ALL"); err != nil {
		t.Fatal(err)
	}
	conn.ForgetStatements()
	if _, err := conn.Prepare(ctx, "discarded", "SELECT 3"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, "discarded").Scan(&n); err != nil || n != 3 {
		t.Fatalf("run after DISCARD ALL and Prepare = %d, %v", n, err)
	}
}

// TestLivePrepareCancelled proves a Prepare whose context ends while the
// server's answer is on its way closes the connection, as every exchange cut
// short does, and holds nothing under the name: half read, the answer would
// have been taken by the next exchange as its own.
func TestLivePrepareCancelled(t *testing.T) {
	var cancel atomic.Pointer[context.CancelFunc]
	conn := relayedConn(t, Options{}, func([]byte) {}, func([]byte) {
		if stop := cancel.Swap(nil); stop != nil {
			(*stop)()
			time.Sleep(200 * time.Millisecond)
		}
	})
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	cancel.Store(&stop)
	if _, err := conn.Prepare(ctx, "cut_short", "SELECT 1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Prepare() cut short = %v, want context.Canceled", err)
	}
	if !conn.IsClosed() {
		t.Fatal("the connection is open after a Prepare cut short")
	}
	if conn.prepared["cut_short"] != nil {
		t.Fatal("a Prepare cut short is held")
	}
}

// TestLivePrepareBehindPooler proves a statement prepared and run inside one
// transaction works behind a pooler in transaction mode, which is the one way
// it can: the transaction holds one server connection throughout.
func TestLivePrepareBehindPooler(t *testing.T) {
	dsn := os.Getenv("POOLER_URL")
	if dsn == "" {
		t.Skip("POOLER_URL not set")
	}
	ctx := t.Context()
	conn, err := Connect(ctx, dsn, notKept[0])
	if err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Prepare(ctx, "pooled", "SELECT $1::int4 + 1"); err != nil {
		t.Fatalf("Tx.Prepare() behind the pooler = %v", err)
	}
	var n int
	if err := tx.QueryRow(ctx, "pooled", 1).Scan(&n); err != nil || n != 2 {
		t.Fatalf("run behind the pooler = %d, %v", n, err)
	}
	if err := conn.Deallocate(ctx, "pooled"); err != nil {
		t.Fatalf("Deallocate() behind the pooler = %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
