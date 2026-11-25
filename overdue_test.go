// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// A statement run under a context that cannot end sets no deadline on the
// socket, and nothing stood ready for one that timed out all the same, on a
// deadline left there before it. The error came back, the connection stayed
// open and not busy with the statement sent, and whoever used it next was
// answered with that statement's replies, as was everyone after them: a pool
// handed the connection on, and each caller got the result of the one before.
//
// Every kind of exchange has to end the same way: closed.
func TestLiveStatementEndedByALeftoverDeadlineClosesTheConnection(t *testing.T) {
	bg := context.Background()
	table := liveTable(t, liveConn(t, Options{}))
	one := func() []Query { return []Query{{SQL: "SELECT $1::int4", Arguments: []any{1}}} }

	kinds := []struct {
		name string
		run  func(conn *Conn) error
	}{
		{"Exec", func(conn *Conn) error {
			_, err := conn.Exec(bg, "SELECT 1")
			return err
		}},
		{"Exec with an argument", func(conn *Conn) error {
			_, err := conn.Exec(bg, "SELECT $1::int4", 1)
			return err
		}},
		{"Query", func(conn *Conn) error {
			rows, err := conn.Query(bg, "SELECT g FROM generate_series(1, $1::int4) g", 3)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
			}
			return rows.Err()
		}},
		{"QueryRow", func(conn *Conn) error {
			var n int
			return conn.QueryRow(bg, "SELECT $1::int4", 1).Scan(&n)
		}},
		{"the first statement of a transaction", func(conn *Conn) error {
			tx, err := conn.Begin(bg)
			if err != nil {
				return err
			}
			_, err = tx.Exec(bg, "SELECT 1")
			return err
		}},
		{"SendBatch", func(conn *Conn) error {
			results, err := conn.SendBatch(bg, one())
			if err != nil {
				return err
			}
			_, err = results.Exec()
			return errors.Join(err, results.Close())
		}},
		{"SendPipeline", func(conn *Conn) error {
			results, err := conn.SendPipeline(bg, one())
			if err != nil {
				return err
			}
			_, err = results.Exec()
			return errors.Join(err, results.Close())
		}},
		{"CopyFrom", func(conn *Conn) error {
			_, err := conn.CopyFrom(bg, []string{table[1 : len(table)-1]}, []string{"id", "note"}, CopyFromRows([][]any{{1, "a"}}))
			return err
		}},
	}
	for _, kind := range kinds {
		t.Run(kind.name, func(t *testing.T) {
			conn := liveConn(t, Options{})
			if err := conn.Wire().NetConn().SetReadDeadline(time.Unix(1, 0)); err != nil {
				t.Fatalf("SetReadDeadline() = %v", err)
			}
			err := kind.run(conn)
			if !wire.IsTimeout(err) {
				t.Fatalf("got %v, want the timeout", err)
			}
			if !conn.IsClosed() {
				t.Fatalf("left open after %v, busy %v, with replies on their way", err, conn.Busy())
			}
		})
	}
}

// What the leftover deadline came from: a wait for a notification that ended
// with its context. Statements run under a context that cannot end, between
// waits that time out, used to fail within a few hundred rounds.
func TestLiveStatementsBetweenWaitsThatTimeOut(t *testing.T) {
	if testing.Short() {
		t.Skip("a few seconds of waits")
	}
	bg := context.Background()
	conn := liveConn(t, Options{})
	if _, err := conn.Exec(bg, "LISTEN caduceus_nobody_notifies"); err != nil {
		t.Fatalf("LISTEN = %v", err)
	}
	for round := range 3000 {
		ctx, cancel := context.WithTimeout(bg, 300*time.Microsecond)
		_, err := conn.WaitForNotification(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("round %d: WaitForNotification() = %v, want the context's deadline", round, err)
		}
		var n int
		if err := conn.QueryRow(bg, "SELECT $1::int4", round).Scan(&n); err != nil || n != round {
			t.Fatalf("round %d: QueryRow() = %d, %v", round, n, err)
		}
	}
}
