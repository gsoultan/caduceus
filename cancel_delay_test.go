// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// TestLiveCancelDelayKeepsTheConnection cancels a statement with
// wire.Config.CancelDelay set: the statement fails with the server's 57014,
// the error still says the context ended, and the session goes on, its
// transaction with it.
func TestLiveCancelDelayKeepsTheConnection(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := wire.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.CancelDelay = 5 * time.Second
	for _, options := range []Options{{}, {StatementCacheCapacity: -1}} {
		conn, err := ConnectConfig(t.Context(), config, options)
		if err != nil {
			t.Fatalf("ConnectConfig() = %v", err)
		}
		defer conn.Close(context.Background())
		pid := conn.Wire().PID()

		short, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		_, err = conn.Exec(short, "SELECT pg_sleep(30)")
		cancel()
		var serverErr *wire.Error
		if !errors.As(err, &serverErr) || serverErr.Code != "57014" || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cancelled Exec = %v, want 57014 and DeadlineExceeded", err)
		}
		if conn.IsClosed() {
			t.Fatal("the connection was closed")
		}

		// A cancelled query's rows, inside a transaction: the transaction is
		// failed, not lost, and rolls back on the same backend.
		tx, err := conn.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), "CREATE TEMP TABLE kept (id int)"); err != nil {
			t.Fatal(err)
		}
		short, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
		rows, err := tx.Query(short, "SELECT pg_sleep(30)")
		if err == nil {
			for rows.Next() {
			}
			rows.Close()
			err = rows.Err()
		}
		cancel()
		if !errors.As(err, &serverErr) || serverErr.Code != "57014" {
			t.Fatalf("cancelled Query = %v, want 57014", err)
		}
		if conn.TxStatus() != 'E' {
			t.Fatalf("TxStatus() = %c, want E", conn.TxStatus())
		}
		if err := tx.Rollback(t.Context()); err != nil {
			t.Fatalf("Rollback() = %v", err)
		}
		var backend uint32
		if err := conn.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&backend); err != nil || backend != pid {
			t.Fatalf("after the cancel: backend %d, %v; want %d", backend, err, pid)
		}
		// Without a deadline left on the socket, a long statement still runs.
		if _, err := conn.Exec(t.Context(), "SELECT pg_sleep(0.2)"); err != nil {
			t.Fatalf("Exec() = %v", err)
		}
	}
}
