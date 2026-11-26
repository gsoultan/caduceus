// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// What never reached the server says so, and what did does not: a pool or a
// caller can run the first again without running anything twice.
func TestLiveSafeToRetry(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()

	rows, err := conn.Query(ctx, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "SELECT 1"); !errors.Is(err, ErrConnBusy) || !wire.SafeToRetry(err) {
		t.Errorf("Exec on a busy connection = %v, safe to retry %v", err, wire.SafeToRetry(err))
	}
	rows.Close()

	// Arguments that do not fit a statement already known fail before
	// anything is written.
	if _, err := conn.Exec(ctx, "SELECT $1::int8", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "SELECT $1::int8", 1, 2); err == nil || !wire.SafeToRetry(err) {
		t.Errorf("an argument refused = %v, safe to retry %v", err, wire.SafeToRetry(err))
	}

	// A context over before the statement is sent: nothing is sent, and the
	// connection is left as it was. It used to be written, and then closed.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := conn.Exec(cancelled, "SELECT 1"); !errors.Is(err, context.Canceled) || !wire.SafeToRetry(err) {
		t.Errorf("Exec with a context already over = %v, safe to retry %v", err, wire.SafeToRetry(err))
	}
	if conn.IsClosed() {
		t.Fatal("a context over before anything was sent closed the connection")
	}

	// What the server refused, it was sent.
	if _, err := conn.Exec(ctx, "SELECT * FROM no_such_table_here"); err == nil || wire.SafeToRetry(err) {
		t.Errorf("a server error = %v, safe to retry %v", err, wire.SafeToRetry(err))
	}
	// What a deadline cut off was sent and may have run.
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	_, err = conn.Exec(short, "SELECT pg_sleep(5)")
	if !wire.Timeout(err) || wire.SafeToRetry(err) {
		t.Errorf("a statement cut off = %v, timeout %v, safe to retry %v", err, wire.Timeout(err), wire.SafeToRetry(err))
	}
	if _, err := conn.Exec(ctx, "SELECT 1"); !errors.Is(err, wire.ErrClosed) || !wire.SafeToRetry(err) {
		t.Errorf("Exec on a closed connection = %v, safe to retry %v", err, wire.SafeToRetry(err))
	}
}

// A connection that fails says where it was connecting to, and never the
// password, and still unwraps to the server's error.
func TestLiveConnectError(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := wire.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = "caduceus_no_such_database"
	config.Password = "caduceus-secret-password"
	_, err = ConnectConfig(t.Context(), config, Options{})
	var connectErr *wire.ConnectError
	if !errors.As(err, &connectErr) || connectErr.Database != config.Database || connectErr.Host != config.Host {
		t.Fatalf("Connect = %v, want a ConnectError for the database", err)
	}
	var server *wire.Error
	if !errors.As(err, &server) {
		t.Errorf("Connect = %v, want it to unwrap to the server's error", err)
	}
	if strings.Contains(err.Error(), config.Password) {
		t.Errorf("Connect = %v names the password", err)
	}
	if !strings.Contains(err.Error(), "database=caduceus_no_such_database") {
		t.Errorf("Connect = %v, want it to name the database", err)
	}
}
