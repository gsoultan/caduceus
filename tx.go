// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"strconv"

	"github.com/gsoultan/caduceus/wire"
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
//
// Begin sends nothing. BEGIN travels with the transaction's first statement,
// in the same write, which saves every transaction a round trip; a
// transaction that runs no statement sends nothing at all, and its Commit and
// Rollback are free. PostgreSQL takes its snapshot at the first statement
// either way. What differs from a BEGIN sent at once: a connection that has
// died is found by the first statement, not by Begin; a BEGIN the server
// refuses is reported by that statement, as BeginTx describes; and
// transaction_timestamp() is when that statement arrived.
//
// A Tx is spent once it has been settled, a later transaction has begun on the
// connection, or the connection has been unwound for its next user. Every
// method then returns ErrTxClosed and sends nothing, so a Tx that outlives its
// transaction, or a savepoint that outlives the transaction around it, cannot
// commit, roll back or run a statement in one that is not its own.
//
// A transaction ended any other way, by a COMMIT or ROLLBACK run as a
// statement, is not noticed. Its Tx stays live: what it runs next runs outside
// any transaction, and settling it sends a COMMIT or ROLLBACK that has nothing
// to end. A pool handing connections on must therefore unwind every one it
// takes back, which costs nothing where nothing is open, or keep its own
// account of who holds each.
type Tx struct {
	conn *Conn
	// epoch is the connection's when the transaction began; a savepoint has
	// its transaction's.
	epoch uint64
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
//
// If the server refuses the BEGIN, which it does for characteristics it
// cannot give, such as a serializable transaction on a hot standby, the
// transaction's first statement returns the server's error and does not run.
// Until the transaction is settled, everything after it returns
// ErrTxNotBegun, on the Tx and on the connection alike: no transaction is open
// on the server, and a statement let through would run on its own and stay
// committed.
func (s *Conn) BeginTx(ctx context.Context, options TxOptions) (*Tx, error) {
	sql, err := options.beginSQL()
	if err != nil {
		return nil, err
	}
	if err := s.idle(ctx); err != nil {
		return nil, err
	}
	s.epoch++
	s.pending, s.refused = sql, false
	return &Tx{conn: s, epoch: s.epoch}, nil
}

// Unwind rolls back whatever transaction is open on the connection and spends
// every Tx begun on it. It is for a pool taking a connection back from a
// caller that left a transaction unsettled: the next caller must find neither
// the transaction nor a Tx that can still reach the connection. A transaction
// whose BEGIN was never sent, or was refused, has nothing to roll back and
// costs nothing.
//
// On a connection in the middle of a result it does nothing and returns
// ErrConnBusy. What is open on the server is not known until the result has
// been read to its end, and a pool does better to close such a connection
// than to read it.
func (s *Conn) Unwind(ctx context.Context) error {
	if s.open {
		return ErrConnBusy
	}
	s.epoch++
	s.pending, s.refused = "", false
	if s.conn.TxStatus() == wire.TxIdle {
		return nil
	}
	_, err := s.exec(ctx, "ROLLBACK", nil)
	return err
}

// idle reports why a transaction cannot begin on the connection now, or nil.
func (s *Conn) idle(ctx context.Context) error {
	if s.open {
		return ErrConnBusy
	}
	if s.conn.IsClosed() {
		return wire.ErrClosed
	}
	if s.txStatus() != wire.TxIdle {
		return ErrInTransaction
	}
	return ctx.Err()
}

// unsent settles a transaction whose BEGIN was never sent or did not take
// effect, which takes no round trip, and reports whether it was one. A refusal
// is lifted with it: the connection ran nothing while the caller still held
// the transaction for open, and is free again now.
func (t *Tx) unsent() bool {
	if t.savepoint != "" || (t.conn.pending == "" && !t.conn.refused) {
		return false
	}
	t.conn.pending, t.conn.refused = "", false
	return true
}

// spent reports whether the transaction is over: settled here, or left behind
// by the connection, whose epoch has moved on. The connection's pending BEGIN
// and its refusal are then another transaction's, and not this one's to read.
func (t *Tx) spent() bool {
	return t.done || t.epoch != t.conn.epoch
}

// settle marks the transaction settled. Settling the outermost one moves the
// connection's epoch on, which spends the savepoints inside it.
func (t *Tx) settle() {
	t.done = true
	if t.savepoint == "" {
		t.conn.epoch++
	}
}

// usable returns why the transaction cannot run a statement, or nil. One
// whose BEGIN was refused is turned away by the connection, as is whatever
// else is run on it until the transaction is settled.
func (t *Tx) usable() error {
	if t.spent() {
		return ErrTxClosed
	}
	return nil
}

// Begin starts a nested transaction as a savepoint. Committing it releases
// the savepoint; rolling it back undoes only what ran since, and leaves the
// enclosing transaction usable even after an error inside the savepoint.
func (t *Tx) Begin(ctx context.Context) (*Tx, error) {
	if err := t.usable(); err != nil {
		return nil, err
	}
	depth := t.depth + 1
	name := "caduceus_sp_" + strconv.Itoa(depth)
	if _, err := t.conn.exec(ctx, "SAVEPOINT "+name, nil); err != nil {
		return nil, err
	}
	return &Tx{conn: t.conn, epoch: t.epoch, savepoint: name, depth: depth}, nil
}

// Commit commits the transaction, or releases the savepoint. If the
// transaction had already failed, the server rolls it back instead and Commit
// returns ErrTxCommitRollback.
func (t *Tx) Commit(ctx context.Context) error {
	if t.spent() {
		return ErrTxClosed
	}
	t.settle()
	// Read before unsent lifts it.
	refused := t.conn.refused
	unsent := t.unsent()
	switch {
	case t.conn.IsClosed():
		// Said of a transaction whose BEGIN was never sent as well: a
		// statement tried in it since the connection closed has failed, and
		// nil would say it had been committed.
		return wire.ErrClosed
	case refused:
		return ErrTxNotBegun
	case unsent:
		return nil
	}
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
	if t.spent() {
		return ErrTxClosed
	}
	t.settle()
	if unsent := t.unsent(); unsent || t.conn.IsClosed() {
		// One the server never began has nothing to undo, and it ends a
		// transaction with the session.
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
	if err := t.usable(); err != nil {
		return "", err
	}
	return t.conn.exec(ctx, sql, args)
}

// Query runs a query inside the transaction.
func (t *Tx) Query(ctx context.Context, sql string, args ...any) (*Rows, error) {
	if err := t.usable(); err != nil {
		return nil, err
	}
	return t.conn.query(ctx, sql, args)
}

// QueryRow runs a query inside the transaction expected to return at most one row.
func (t *Tx) QueryRow(ctx context.Context, sql string, args ...any) *Row {
	if err := t.usable(); err != nil {
		return &Row{err: err}
	}
	return t.conn.QueryRow(ctx, sql, args...)
}

// SendBatch sends a batch inside the transaction.
func (t *Tx) SendBatch(ctx context.Context, queries []Query) (*BatchResults, error) {
	if err := t.usable(); err != nil {
		return nil, err
	}
	return t.conn.SendBatch(ctx, queries)
}

// CopyFrom copies rows into a table inside the transaction.
func (t *Tx) CopyFrom(ctx context.Context, table, columns []string, rows CopySource) (int64, error) {
	if err := t.usable(); err != nil {
		return 0, err
	}
	return t.conn.CopyFrom(ctx, table, columns, rows)
}

// Conn returns the connection the transaction runs on.
func (t *Tx) Conn() *Conn { return t.conn }

// SendPipeline sends a pipeline inside the transaction. Every statement of it
// is then part of the transaction, and the first the server refuses aborts
// it, as any failed statement in a transaction does: the statements after it
// do not run and report an error too. This is the fastest way to run many
// statements, since the server answers them a window at a time.
func (t *Tx) SendPipeline(ctx context.Context, queries []Query) (*PipelineResults, error) {
	if err := t.usable(); err != nil {
		return nil, err
	}
	return t.conn.SendPipeline(ctx, queries)
}
