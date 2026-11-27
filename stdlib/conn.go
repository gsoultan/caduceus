// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strconv"
	"time"

	"github.com/gsoultan/caduceus"
)

// teardownTimeout bounds what database/sql calls without a context to undo
// something: closing a connection, deallocating a statement, rolling back.
const teardownTimeout = 10 * time.Second

// Conn is one connection, which database/sql hands to one caller at a time.
// sql.Conn's Raw gives it, and Conn the *caduceus.Conn underneath.
type Conn struct {
	conn *caduceus.Conn
	// prepared counts the statements Prepare has named on this connection.
	prepared int
}

var (
	_ driver.Conn               = (*Conn)(nil)
	_ driver.ConnPrepareContext = (*Conn)(nil)
	_ driver.ConnBeginTx        = (*Conn)(nil)
	_ driver.ExecerContext      = (*Conn)(nil)
	_ driver.QueryerContext     = (*Conn)(nil)
	_ driver.Pinger             = (*Conn)(nil)
	_ driver.SessionResetter    = (*Conn)(nil)
	_ driver.Validator          = (*Conn)(nil)
	_ driver.NamedValueChecker  = (*Conn)(nil)
)

// Conn returns the caduceus connection, for what database/sql has no word
// for. It must be used only inside sql.Conn's Raw, which holds the connection
// for the caller, and left idle: no rows open, no transaction begun.
func (c *Conn) Conn() *caduceus.Conn { return c.conn }

// Prepare is PrepareContext without a context.
func (c *Conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

// PrepareContext prepares query on the server, under a name of its own.
func (c *Conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if c.conn.IsClosed() {
		return nil, driver.ErrBadConn
	}
	c.prepared++
	name := "stdlib_" + strconv.Itoa(c.prepared)
	description, err := c.conn.Prepare(ctx, name, query)
	if err != nil {
		return nil, err
	}
	return &stmt{conn: c.conn, name: name, inputs: len(description.ParamOIDs)}, nil
}

// Close closes the connection, gracefully within teardownTimeout. It is
// idempotent.
func (c *Conn) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	return c.conn.Close(ctx)
}

// Begin is BeginTx with the default options.
func (c *Conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx begins a transaction. Its BEGIN is sent with the first statement
// in it, which saves a round trip.
func (c *Conn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	txOptions, err := txOptions(options)
	if err != nil {
		return nil, err
	}
	if c.conn.IsClosed() {
		return nil, driver.ErrBadConn
	}
	begun, err := c.conn.BeginTx(ctx, txOptions)
	if err != nil {
		return nil, err
	}
	return tx{tx: begun}, nil
}

// ExecContext runs a statement. Without arguments it may hold several,
// separated by semicolons.
func (c *Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.conn.IsClosed() {
		return nil, driver.ErrBadConn
	}
	tag, err := c.conn.Exec(ctx, query, values(args)...)
	if err != nil {
		return nil, err
	}
	return result{tag: tag}, nil
}

// QueryContext runs a query, whose rows are read off the connection as
// database/sql iterates them.
func (c *Conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.conn.IsClosed() {
		return nil, driver.ErrBadConn
	}
	found, err := c.conn.Query(ctx, query, values(args)...)
	if err != nil {
		return nil, err
	}
	return &rows{rows: found}, nil
}

// Ping makes a round trip to the server. A connection that is found dead
// reports driver.ErrBadConn, so that database/sql discards it.
func (c *Conn) Ping(ctx context.Context) error {
	if c.conn.IsClosed() {
		return driver.ErrBadConn
	}
	err := c.conn.Ping(ctx)
	if err != nil && c.conn.IsClosed() {
		return driver.ErrBadConn
	}
	return err
}

// ResetSession is called before the connection is handed to its next
// caller, and discards it if it has died while idle: a server that ended the
// session, or restarted, has closed the socket or said why, which CheckConn
// sees without a round trip. The caller then gets a fresh connection rather
// than the error.
func (c *Conn) ResetSession(context.Context) error {
	if c.conn.CheckConn() != nil {
		return driver.ErrBadConn
	}
	return nil
}

// IsValid reports whether the connection may go back to the pool.
func (c *Conn) IsValid() bool { return !c.conn.IsClosed() }

// CheckNamedValue lets every argument through to caduceus, which binds more
// than database/sql's own conversion would: slices, structs for json,
// caduceus.NamedArgs. A driver.Valuer is called by caduceus, and a nil
// pointer to one is NULL.
func (*Conn) CheckNamedValue(arg *driver.NamedValue) error {
	return checkArg(arg)
}

// checkArg refuses the arguments PostgreSQL has nothing for.
func checkArg(arg *driver.NamedValue) error {
	if arg.Name != "" {
		return ErrNamedArg
	}
	if _, out := arg.Value.(sql.Out); out {
		return ErrOutArg
	}
	return nil
}

// values are the arguments, in order.
func values(args []driver.NamedValue) []any {
	if len(args) == 0 {
		return nil
	}
	values := make([]any, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	return values
}
