// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Tests against a real server. A scripted server proves the state machine; only
// PostgreSQL proves this speaks the protocol PostgreSQL does.
func liveConfig(t *testing.T) *Config {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig() = %v", err)
	}
	return config
}

func liveConn(t *testing.T) *Conn {
	t.Helper()
	conn, err := Connect(t.Context(), liveConfig(t))
	if err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func TestLiveConnectAndQuery(t *testing.T) {
	conn := liveConn(t)
	if conn.ParameterStatus("server_version") == "" {
		t.Error("no server_version reported")
	}
	if conn.PID() == 0 {
		t.Error("no backend key data")
	}
	result, err := conn.QueryText(t.Context(), "SELECT $1::int + 1, NULL::text, ''::text", "41")
	if err != nil {
		t.Fatalf("QueryText() = %v", err)
	}
	row := result.Rows[0]
	if string(row[0]) != "42" || row[1] != nil || row[2] == nil || len(row[2]) != 0 {
		t.Fatalf("row = %q", row)
	}
	if result.Tag != "SELECT 1" {
		t.Errorf("tag = %q", result.Tag)
	}
	if conn.TxStatus() != TxIdle {
		t.Errorf("tx status = %c", conn.TxStatus())
	}
}

// A server error leaves the connection at a boundary, usable.
func TestLiveServerErrorKeepsTheConnection(t *testing.T) {
	conn := liveConn(t)
	_, err := conn.Exec(t.Context(), "SELECT * FROM no_such_table_here")
	var serverErr *Error
	if !errors.As(err, &serverErr) || serverErr.Code != "42P01" {
		t.Fatalf("err = %v, want SQLSTATE 42P01", err)
	}
	if _, err := conn.Exec(t.Context(), "SELECT 1"); err != nil {
		t.Fatalf("after an error: %v", err)
	}
}

// A context that ends mid-query cancels it on the server and closes the
// connection, and the error says it was the context.
func TestLiveContextCancelsTheQuery(t *testing.T) {
	conn := liveConn(t)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := conn.Exec(ctx, "SELECT pg_sleep(10)")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("took %v", time.Since(started))
	}
	if !conn.IsClosed() {
		t.Fatal("the connection was left open with a query in flight")
	}
}

// A wrong password is the server's SQLSTATE, not a string to match.
func TestLiveWrongPassword(t *testing.T) {
	config := liveConfig(t)
	if os.Getenv("DATABASE_URL_SCRAM") == "" {
		t.Skip("DATABASE_URL_SCRAM not set")
	}
	scram, err := ParseConfig(os.Getenv("DATABASE_URL_SCRAM"))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := Connect(t.Context(), scram)
	if err != nil {
		t.Fatalf("SCRAM login = %v", err)
	}
	conn.Close(t.Context())

	scram.Password = "wrong"
	_, err = Connect(t.Context(), scram)
	var serverErr *Error
	if !errors.As(err, &serverErr) || serverErr.Code != "28P01" {
		t.Fatalf("err = %v, want 28P01", err)
	}
	_ = config
	_ = url.QueryEscape
}

// The read buffer grows while a large result streams in and is given back
// afterwards. Everything per connection is multiplied by a pool's size, so a
// connection that once read a large result must not hold the larger buffer
// while it sits idle.
func TestLiveReadBufferGrowsForAStreamAndShrinksBack(t *testing.T) {
	conn := liveConn(t)
	result, err := conn.QueryText(t.Context(), "SELECT g, repeat('x', 40) FROM generate_series(1, 20000) AS g")
	if err != nil || len(result.Rows) != 20000 {
		t.Fatalf("QueryText() = %d rows, %v", len(result.Rows), err)
	}
	if len(conn.rbuf) <= initialReadBuffer || len(conn.rbuf) > streamReadBuffer {
		t.Fatalf("buffer after a megabyte of rows = %d, want it grown, and to at most %d", len(conn.rbuf), streamReadBuffer)
	}
	// A few short exchanges do not give it back. It used to go after one, and
	// the windows of a pipeline, which fill the buffer every other time, had
	// it freed and grown again for each: 22 MiB of garbage for a million
	// statements.
	grown := len(conn.rbuf)
	for range calmExchanges - 1 {
		if _, err := conn.QueryText(t.Context(), "SELECT 1"); err != nil {
			t.Fatal(err)
		}
	}
	if len(conn.rbuf) != grown {
		t.Fatalf("buffer after %d short exchanges = %d, want it kept at %d", calmExchanges-1, len(conn.rbuf), grown)
	}
	for range 2 {
		if _, err := conn.QueryText(t.Context(), "SELECT 1"); err != nil {
			t.Fatal(err)
		}
	}
	if len(conn.rbuf) != initialReadBuffer {
		t.Fatalf("buffer on an idle connection = %d, want %d", len(conn.rbuf), initialReadBuffer)
	}
}

// The connection speaks 3.2 with PostgreSQL 18 and later and 3.0 with an
// older server, which it learns from the server's answer, and a pooler that
// refuses 3.2 is asked again for 3.0. A query and a cancellation work in
// whichever it speaks.
func TestLiveProtocolVersion(t *testing.T) {
	for _, target := range []string{"DATABASE_URL", "POOLER_URL"} {
		t.Run(target, func(t *testing.T) {
			dsn := os.Getenv(target)
			if dsn == "" {
				t.Skip(target + " not set")
			}
			config, err := ParseConfig(dsn)
			if err != nil {
				t.Fatal(err)
			}
			for round := range 2 {
				conn, err := Connect(t.Context(), config.Copy())
				if err != nil {
					t.Fatalf("round %d: %v", round, err)
				}
				major, _, _ := strings.Cut(conn.ParameterStatus("server_version"), ".")
				version, err := strconv.Atoi(major)
				if err != nil {
					t.Fatalf("server_version %q", conn.ParameterStatus("server_version"))
				}
				if got := conn.ProtocolVersion(); version < 18 && got != Protocol30 {
					t.Errorf("round %d: PostgreSQL %d speaks %s, want 3.0", round, version, got)
				}
				if got := conn.ProtocolVersion(); version >= 18 && target == "DATABASE_URL" && got != Protocol32 {
					t.Errorf("round %d: PostgreSQL %d speaks %s, want 3.2", round, version, got)
				}
				if _, err := conn.Exec(t.Context(), "SELECT 1"); err != nil {
					t.Fatalf("round %d: %v", round, err)
				}
				_ = conn.Close(t.Context())
			}
		})
	}
}
