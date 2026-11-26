// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
)

// BeginFunc begins a transaction on db, a Conn or a Tx, and runs fn in it. It
// commits when fn returns nil and rolls back when fn returns an error or
// panics; the panic then goes on. fn's error is returned, joined with a
// rollback's if that fails too. The rollback runs even when ctx has ended, so
// a cancelled request still leaves no transaction open.
func BeginFunc(ctx context.Context, db TxBeginner, fn func(*Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	return runTx(ctx, tx, fn)
}

// BeginTxFunc is BeginFunc with the transaction's options, on a connection.
func BeginTxFunc(ctx context.Context, conn *Conn, options TxOptions, fn func(*Tx) error) error {
	tx, err := conn.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	return runTx(ctx, tx, fn)
}

// runTx runs fn in tx and settles it.
func runTx(ctx context.Context, tx *Tx, fn func(*Tx) error) (err error) {
	// A rollback after a commit, or after a commit that failed, only says
	// ErrTxClosed, so this one is safe to defer whatever happens; it is what
	// undoes the transaction on a panic.
	defer func() {
		if rollback := tx.Rollback(context.WithoutCancel(ctx)); err != nil && rollback != nil && !errors.Is(rollback, ErrTxClosed) {
			err = errors.Join(err, rollback)
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
