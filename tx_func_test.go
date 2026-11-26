// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"testing"
)

// BeginFunc commits what fn did when it returns nil, and rolls it back when
// it returns an error or panics, leaving the connection idle and usable each
// time; inside a Tx it is a savepoint, whose rollback keeps the outer
// transaction's work.
func TestLiveBeginFunc(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)
	insert := func(id int) func(*Tx) error {
		return func(tx *Tx) error {
			_, err := tx.Exec(ctx, "INSERT INTO "+table+" (id) VALUES ($1)", id)
			return err
		}
	}
	failure := errors.New("fn failed")

	if err := BeginFunc(ctx, conn, insert(1)); err != nil {
		t.Fatal(err)
	}
	err := BeginFunc(ctx, conn, func(tx *Tx) error {
		if err := insert(2)(tx); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("BeginFunc = %v, want fn's error alone", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the panic did not go on")
			}
		}()
		_ = BeginFunc(ctx, conn, func(tx *Tx) error {
			_ = insert(3)(tx)
			panic("fn panicked")
		})
	}()
	if got := countRows(t, conn, table); got != 1 {
		t.Fatalf("%d rows, want only the committed one", got)
	}
	if conn.TxStatus() != 'I' || conn.Busy() {
		t.Fatalf("status %c, busy %v after the rollbacks", conn.TxStatus(), conn.Busy())
	}

	err = BeginFunc(ctx, conn, func(outer *Tx) error {
		if err := insert(4)(outer); err != nil {
			return err
		}
		if err := BeginFunc(ctx, outer, func(inner *Tx) error {
			_ = insert(5)(inner)
			return failure
		}); !errors.Is(err, failure) {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, conn, table); got != 2 {
		t.Fatalf("%d rows, want the outer insert kept and the savepoint's undone", got)
	}
}

// A cancelled context still rolls the transaction back: the connection it
// leaves is not in a transaction a pool would hand to the next caller.
func TestLiveBeginFuncRollsBackOnACancelledContext(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx, cancel := context.WithCancel(t.Context())
	err := BeginFunc(ctx, conn, func(tx *Tx) error {
		if _, err := tx.Exec(ctx, "SELECT 1"); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BeginFunc = %v", err)
	}
	if conn.TxStatus() != 'I' {
		t.Fatalf("status %c after a cancelled BeginFunc, want I", conn.TxStatus())
	}
}

// BeginTxFunc begins with the options asked for.
func TestLiveBeginTxFunc(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	err := BeginTxFunc(ctx, conn, TxOptions{IsoLevel: Serializable, ReadOnly: true}, func(tx *Tx) error {
		var level, readOnly string
		if err := tx.QueryRow(ctx, "SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only')").Scan(&level, &readOnly); err != nil {
			return err
		}
		if level != "serializable" || readOnly != "on" {
			t.Errorf("isolation %q, read only %q", level, readOnly)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// CopyFromSlice and CopyFromFunc copy every row they give, and an error from
// either fails the copy with nothing copied.
func TestLiveCopyFromSliceAndFunc(t *testing.T) {
	conn := liveConn(t, Options{})
	ctx := t.Context()
	table := liveTable(t, conn)
	tableName := []string{table[1 : len(table)-1]}
	ids := []int{1, 2, 3}
	n, err := conn.CopyFrom(ctx, tableName, []string{"id", "note"}, CopyFromSlice(len(ids), func(i int) ([]any, error) {
		return []any{ids[i], "slice"}, nil
	}))
	if err != nil || n != 3 {
		t.Fatalf("CopyFromSlice = %d, %v", n, err)
	}
	next := 10
	n, err = conn.CopyFrom(ctx, tableName, []string{"id", "note"}, CopyFromFunc(func() ([]any, error) {
		if next == 13 {
			return nil, nil
		}
		next++
		return []any{next, "func"}, nil
	}))
	if err != nil || n != 3 {
		t.Fatalf("CopyFromFunc = %d, %v", n, err)
	}
	failure := errors.New("source failed")
	for name, source := range map[string]CopySource{
		"slice": CopyFromSlice(5, func(i int) ([]any, error) {
			if i == 2 {
				return nil, failure
			}
			return []any{100 + i, "x"}, nil
		}),
		"func":                       CopyFromFunc(func() ([]any, error) { return nil, failure }),
		"func, a row with the error": CopyFromFunc(func() ([]any, error) { return []any{200, "x"}, failure }),
	} {
		if _, err := conn.CopyFrom(ctx, tableName, []string{"id", "note"}, source); !errors.Is(err, failure) {
			t.Fatalf("%s: CopyFrom = %v, want the source's error", name, err)
		}
	}
	if got := countRows(t, conn, table); got != 6 {
		t.Fatalf("%d rows, want 6", got)
	}
}

// Each part of an Identifier is quoted on its own, so a schema and a table
// qualify each other and a dot inside a part stays in that part.
func TestIdentifierSanitize(t *testing.T) {
	tests := []struct {
		ident Identifier
		want  string
	}{
		{Identifier{"users"}, `"users"`},
		{Identifier{"public", "users"}, `"public"."users"`},
		{Identifier{"my.schema", `we"ird`}, `"my.schema"."we""ird"`},
	}
	for _, tt := range tests {
		if got := tt.ident.Sanitize(); got != tt.want {
			t.Errorf("%v.Sanitize() = %s, want %s", []string(tt.ident), got, tt.want)
		}
	}
}
