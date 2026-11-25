// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"strconv"
)

// Tx is a transaction, or a savepoint inside one.
//
// Commit and Rollback each settle it exactly once; the second call returns
// ErrTxClosed and sends nothing, so the idiom of a deferred Rollback beside a
// Commit on the happy path is safe:
//
//	tx, err := conn.Begin(ctx)
//	if err != nil { return err }
//	defer tx.Rollback(ctx)
//	if _, err := tx.Exec(ctx, "UPDATE accounts SET balance = balance - $1 WHERE id = $2", 10, 1); err != nil {
//		return err
//	}
//	return tx.Commit(ctx)
//
// A Tx runs its statements on the connection it began on, which stays usable
// directly as well; the Tx only adds the bookkeeping of being settled.
type Tx struct {
	conn *Conn
	// savepoint names the savepoint this Tx stands for, empty for the
	// outermost transaction.
	savepoint string
	depth     int
	done      bool
}

// Begin starts a transaction with the server's default characteristics.
func (s *Conn) Begin(ctx context.Context) (*Tx, error) {
	return s.BeginTx(ctx, TxOptions{})
}

// BeginTx starts a transaction with the given characteristics. It returns
// ErrInTransaction if one is already open on the connection; use Tx.Begin for
// a nested one.
func (s *Conn) BeginTx(ctx context.Context, options TxOptions) (*Tx, error) {
	sql, err := options.beginSQL()
	if err != nil {
		return nil, err
	}
	if s.conn.TxStatus() != 'I' && !s.open {
		return nil, ErrInTransaction
	}
	if _, err := s.exec(ctx, sql, nil); err != nil {
		return nil, err
	}
	return &Tx{conn: s}, nil
}

// Begin starts a nested transaction as a savepoint. Committing it releases
// the savepoint; rolling it back undoes only what ran since, and leaves the
// enclosing transaction usable even after an error inside the savepoint.
func (t *Tx) Begin(ctx context.Context) (*Tx, error) {
	if t.done {
		return nil, ErrTxClosed
	}
	depth := t.depth + 1
	name := "caduceus_sp_" + strconv.Itoa(depth)
	if _, err := t.conn.exec(ctx, "SAVEPOINT "+name, nil); err != nil {
		return nil, err
	}
	return &Tx{conn: t.conn, savepoint: name, depth: depth}, nil
}

// Commit commits the transaction, or releases the savepoint. If the
// transaction had already failed, the server rolls it back instead and Commit
// returns ErrTxCommitRollback.
func (t *Tx) Commit(ctx context.Context) error {
	if t.done {
		return ErrTxClosed
	}
	t.done = true
	if t.savepoint != "" {
		_, err := t.conn.exec(ctx, "RELEASE SAVEPOINT "+t.savepoint, nil)
		return err
	}
	tag, err := t.conn.exec(ctx, "COMMIT", nil)
	if err != nil {
		return err
	}
	if tag == "ROLLBACK" {
		return ErrTxCommitRollback
	}
	return nil
}

// Rollback rolls the transaction back, or back to the savepoint. Called after
// Commit it returns ErrTxClosed, which a deferred Rollback can ignore.
func (t *Tx) Rollback(ctx context.Context) error {
	if t.done {
		return ErrTxClosed
	}
	t.done = true
	if t.conn.IsClosed() {
		// The server ends the transaction with the session.
		return nil
	}
	if t.savepoint != "" {
		_, err := t.conn.exec(ctx, "ROLLBACK TO SAVEPOINT "+t.savepoint+"; RELEASE SAVEPOINT "+t.savepoint, nil)
		return err
	}
	_, err := t.conn.exec(ctx, "ROLLBACK", nil)
	return err
}

// Exec runs a statement inside the transaction.
func (t *Tx) Exec(ctx context.Context, sql string, args ...any) (string, error) {
	if t.done {
		return "", ErrTxClosed
	}
	return t.conn.exec(ctx, sql, args)
}

// Query runs a query inside the transaction.
func (t *Tx) Query(ctx context.Context, sql string, args ...any) (*Rows, error) {
	if t.done {
		return nil, ErrTxClosed
	}
	return t.conn.query(ctx, sql, args)
}

// QueryRow runs a query inside the transaction expected to return at most one row.
func (t *Tx) QueryRow(ctx context.Context, sql string, args ...any) *Row {
	if t.done {
		return &Row{err: ErrTxClosed}
	}
	return t.conn.QueryRow(ctx, sql, args...)
}

// SendBatch sends a batch inside the transaction.
func (t *Tx) SendBatch(ctx context.Context, queries []Query) (*BatchResults, error) {
	if t.done {
		return nil, ErrTxClosed
	}
	return t.conn.SendBatch(ctx, queries)
}

// CopyFrom copies rows into a table inside the transaction.
func (t *Tx) CopyFrom(ctx context.Context, table, columns []string, rows CopySource) (int64, error) {
	if t.done {
		return 0, ErrTxClosed
	}
	return t.conn.CopyFrom(ctx, table, columns, rows)
}

// Conn returns the connection the transaction runs on.
func (t *Tx) Conn() *Conn { return t.conn }
