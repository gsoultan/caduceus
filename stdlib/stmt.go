// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib

import (
	"context"
	"database/sql/driver"

	"github.com/gsoultan/caduceus"
)

// stmt is a statement prepared on the server, run by its name.
type stmt struct {
	conn   *caduceus.Conn
	name   string
	inputs int
	closed bool
}

var (
	_ driver.Stmt              = (*stmt)(nil)
	_ driver.StmtExecContext   = (*stmt)(nil)
	_ driver.StmtQueryContext  = (*stmt)(nil)
	_ driver.NamedValueChecker = (*stmt)(nil)
)

// Close deallocates the statement on the server, within teardownTimeout. It
// is idempotent, and on a connection that has died there is nothing to
// deallocate.
func (s *stmt) Close() error {
	if s.closed || s.conn.IsClosed() {
		s.closed = true
		return nil
	}
	s.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	return s.conn.Deallocate(ctx, s.name)
}

// NumInput is how many parameters the statement takes.
func (s *stmt) NumInput() int { return s.inputs }

// Exec is ExecContext without a context.
func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), named(args))
}

// Query is QueryContext without a context.
func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), named(args))
}

// ExecContext runs the statement.
func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if s.conn.IsClosed() {
		return nil, driver.ErrBadConn
	}
	tag, err := s.conn.Exec(ctx, s.name, values(args)...)
	if err != nil {
		return nil, err
	}
	return result{tag: tag}, nil
}

// QueryContext runs the statement for its rows.
func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if s.conn.IsClosed() {
		return nil, driver.ErrBadConn
	}
	found, err := s.conn.Query(ctx, s.name, values(args)...)
	if err != nil {
		return nil, err
	}
	return &rows{rows: found}, nil
}

// CheckNamedValue is the connection's.
func (*stmt) CheckNamedValue(arg *driver.NamedValue) error {
	return checkArg(arg)
}

// named numbers positional arguments, as the context methods take them.
func named(args []driver.Value) []driver.NamedValue {
	values := make([]driver.NamedValue, len(args))
	for i, arg := range args {
		values[i] = driver.NamedValue{Ordinal: i + 1, Value: arg}
	}
	return values
}
