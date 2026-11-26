// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"errors"
	"testing"
)

// Each queued statement's callback gets that statement's result, in order,
// on a Conn and in a Tx, and one without a callback is run and its result
// dropped.
func TestLiveBatchQueue(t *testing.T) {
	for _, options := range append([]Options{{}}, notKept...) {
		conn := liveConn(t, options)
		ctx := t.Context()
		table := liveTable(t, conn)
		var b Batch
		b.Queue("INSERT INTO "+table+" (id, note) VALUES ($1, $2)", 1, "a")
		var inserted CommandTag
		b.Queue("INSERT INTO "+table+" (id, note) VALUES ($1, $2), ($3, $4)", 2, "b", 3, "c").Exec(func(tag CommandTag) error {
			inserted = tag
			return nil
		})
		var count int
		b.Queue("SELECT count(*) FROM " + table).QueryRow(func(row *Row) error { return row.Scan(&count) })
		var notes []string
		b.Queue("SELECT note FROM " + table + " ORDER BY id").Query(func(rows *Rows) error {
			var err error
			notes, err = CollectRows(rows, RowTo[string])
			return err
		})
		if b.Len() != 4 {
			t.Fatalf("Len = %d", b.Len())
		}
		if err := b.Run(ctx, conn); err != nil {
			t.Fatal(err)
		}
		if inserted.RowsAffected() != 2 || count != 3 || len(notes) != 3 || notes[2] != "c" {
			t.Fatalf("tag %q, count %d, notes %v", inserted, count, notes)
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var inTx Batch
		var one int
		inTx.Queue("SELECT 1").QueryRow(func(row *Row) error { return row.Scan(&one) })
		if err := inTx.Run(ctx, tx); err != nil || one != 1 {
			t.Fatalf("in a Tx: %d, %v", one, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// The first error stops the callbacks after it, whether a callback returned
// it or a statement failed, and the rest of the batch is still read: the
// connection is free for the next statement when Run returns.
func TestLiveBatchQueueStopsAtTheFirstError(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	failure := errors.New("callback failed")
	later := false

	var b Batch
	b.Queue("SELECT 1").QueryRow(func(*Row) error { return failure })
	b.Queue("SELECT 2").QueryRow(func(*Row) error { later = true; return nil })
	if err := b.Run(ctx, conn); !errors.Is(err, failure) || later {
		t.Fatalf("Run = %v, later callback ran: %v", err, later)
	}

	var failing Batch
	failing.Queue("SELECT 1/0")
	failing.Queue("SELECT 2").QueryRow(func(*Row) error { later = true; return nil })
	var server interface{ SQLState() string }
	if err := failing.Run(ctx, conn); !errors.As(err, &server) || server.SQLState() != "22012" || later {
		t.Fatalf("Run = %v, later callback ran: %v", err, later)
	}

	var n int
	if err := conn.QueryRow(ctx, "SELECT 3").Scan(&n); err != nil || n != 3 || conn.Busy() {
		t.Fatalf("after the failed batches: %d, %v, busy %v", n, err, conn.Busy())
	}
	var empty Batch
	if err := empty.Run(ctx, conn); !errors.Is(err, ErrEmptyBatch) {
		t.Fatalf("an empty batch = %v", err)
	}
}
