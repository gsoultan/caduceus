// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"

	"github.com/gsoultan/caduceus/wire"
)

// Connect parses connString, connects, and returns a Conn on the connection.
//
// connString is a URL (postgres://user:pass@host:port/db?sslmode=require) or
// key=value pairs, and the PG* environment variables and the password file fill
// in what it leaves out, as they do for libpq.
func Connect(ctx context.Context, connString string, options Options) (*Conn, error) {
	config, err := wire.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	return ConnectConfig(ctx, config, options)
}

// ConnectConfig connects with a parsed configuration. A pool that opens many
// connections parses once and passes a Copy of the config to each.
func ConnectConfig(ctx context.Context, config *wire.Config, options Options) (*Conn, error) {
	tracer, _ := options.Tracer.(ConnectTracer)
	if tracer == nil {
		return connect(ctx, config, options)
	}
	ctx = tracer.TraceConnectStart(ctx, TraceConnectStartData{Config: config})
	conn, err := connect(ctx, config, options)
	tracer.TraceConnectEnd(ctx, TraceConnectEndData{Conn: conn, Err: err})
	return conn, err
}

// connect is ConnectConfig without its tracer.
func connect(ctx context.Context, config *wire.Config, options Options) (*Conn, error) {
	conn, err := wire.Connect(ctx, config)
	if err != nil {
		return nil, err
	}
	return New(conn, options), nil
}

// New runs queries on a connection that is already open and idle.
//
// A Conn belongs to one goroutine at a time. It has no lock: a pool hands it to
// one caller, and an application that shares one between goroutines must
// serialise them itself.
func New(conn *wire.Conn, options Options) *Conn {
	trace := newTracers(options.Tracer)
	if n := options.statements(); n > 0 {
		return &Conn{conn: conn, cache: newStatementCache(n), trace: trace}
	}
	return &Conn{conn: conn, cache: newStatementCache(options.descriptions()), unnamed: true, trace: trace}
}

// Exec runs a statement and returns its command tag, such as "UPDATE 3",
// whose RowsAffected says how many rows it touched.
//
// Without arguments it goes over the simple protocol, one round trip that
// allows several statements separated by semicolons. With arguments they are
// bound as parameters, never interpolated. sql may instead be the name of a
// statement prepared with Prepare, which is then run with args, any number of
// them, as this method, Query, QueryRow, QueryInto, SendBatch and
// SendPipeline all do.
func (s *Conn) Exec(ctx context.Context, sql string, args ...any) (CommandTag, error) {
	tag, err := s.exec(ctx, sql, args)
	return CommandTag(tag), err
}

// Query runs a query and returns its rows, which are read off the connection
// as they are iterated. The connection is busy until the rows are read to the
// end or closed.
func (s *Conn) Query(ctx context.Context, sql string, args ...any) (*Rows, error) {
	return s.query(ctx, sql, args)
}

// QueryInto is Query into rows the caller supplies, so a type that embeds Rows
// costs one allocation rather than two. rows must not be in use.
func (s *Conn) QueryInto(ctx context.Context, rows *Rows, sql string, args ...any) error {
	*rows = Rows{}
	return s.queryInto(ctx, sql, args, rows)
}

// QueryRow runs a query expected to return at most one row. Its error, if any,
// is reported by the row's Scan.
func (s *Conn) QueryRow(ctx context.Context, sql string, args ...any) *Row {
	row := new(Row)
	row.err = s.queryInto(ctx, sql, args, &row.rows)
	return row
}

// SendBatch sends every query without waiting for each reply and returns their
// results in order: in at most two round trips for a batch that fits one
// window of 32 KiB, and a window at a time, as the results are read, for a
// larger one. The batch runs as one implicit transaction: a statement that
// fails undoes those before it, and it and every statement after it report the
// failure. Close the results before the next call on the connection; Close
// also runs whatever of the batch has not been read.
func (s *Conn) SendBatch(ctx context.Context, queries []Query) (*BatchResults, error) {
	if len(queries) == 0 {
		return nil, ErrEmptyBatch
	}
	tracer := s.trace.batch
	if tracer == nil {
		return s.batch(ctx, queries)
	}
	ctx = tracer.TraceBatchStart(ctx, s, TraceBatchStartData{Queries: queries})
	results, err := s.batch(ctx, queries)
	if err != nil {
		tracer.TraceBatchEnd(ctx, s, TraceBatchEndData{Err: err})
		return nil, err
	}
	results.traced = ctx
	return results, nil
}

// CopyFrom streams rows into a table with COPY and returns how many it copied.
// table is the name, schema-qualified as separate parts if need be, such as
// []string{"public", "users"}. Every name is quoted.
//
// The rows go in COPY's binary format, which the server reads without
// parsing, when every column has a type the driver writes in binary: the
// integers, floats, bool, text, varchar, char, bytea, uuid, date, timestamp,
// timestamptz, interval, json and jsonb. With any other column they go in text.
//
// A value only the server can read, such as a string for an integer column, is
// not refused. From that row on the rows go in text, as a second COPY in the
// same transaction as the first, so the rows are still copied all or none;
// statement-level triggers and constraint checks run once for each of the two.
//
// Outside a transaction CopyFrom begins one of its own and commits it behind
// the rows, or rolls it back if the copy fails. Inside one it is part of the
// caller's, and a copy that fails leaves that transaction failed.
func (s *Conn) CopyFrom(ctx context.Context, table, columns []string, rows CopySource) (int64, error) {
	tracer := s.trace.copy
	if tracer == nil {
		return s.copyFrom(ctx, copyRequest{Table: table, Columns: columns, Rows: rows})
	}
	ctx = tracer.TraceCopyFromStart(ctx, s, TraceCopyFromStartData{TableName: table, ColumnNames: columns})
	copied, err := s.copyFrom(ctx, copyRequest{Table: table, Columns: columns, Rows: rows})
	tracer.TraceCopyFromEnd(ctx, s, TraceCopyFromEndData{Rows: copied, Err: err})
	return copied, err
}

// WaitForNotification blocks until a NOTIFY arrives on a channel the
// connection LISTENs to, or ctx is done. A notification that arrived while
// another query ran is returned first, without reading.
func (s *Conn) WaitForNotification(ctx context.Context) (wire.Notification, error) {
	if s.open {
		return wire.Notification{}, ErrConnBusy
	}
	return s.conn.WaitForNotification(ctx)
}

// Busy reports whether a result is open: rows not read to the end, or a batch
// not closed. A busy connection must not be handed to anyone else.
func (s *Conn) Busy() bool { return s.open }

// TxStatus is the transaction status: 'I' idle, 'T' in a transaction, 'E' in a
// failed transaction. It is the server's as of the last ReadyForQuery, except
// for a transaction begun here: that one is 'T' while its BEGIN waits to be
// sent, and 'E' once the server has refused the BEGIN, until it is settled. A
// pool that takes connections back by this status is therefore right to unwind
// both, and Unwind costs a round trip only where the server has a transaction
// to roll back.
//
// While a result is open, the status is the one from before its statement and
// says nothing of what that statement began; see Busy.
func (s *Conn) TxStatus() byte { return s.txStatus() }

// IsClosed reports whether the connection is closed or broken.
func (s *Conn) IsClosed() bool { return s.conn.IsClosed() }

// ForgetStatements drops everything the connection knows of statements without
// telling the server: the prepared statements it keeps there, those of the
// cache and those prepared with Prepare alike, for when the server has already
// dropped them, as DISCARD ALL does, and what it remembers of statements it
// does not keep there, which was learned in a session that has since been
// reset.
func (s *Conn) ForgetStatements() { s.forgetStatements() }

// Wire returns the underlying protocol connection, for what Conn does not
// wrap. Using it while a result is open corrupts the exchange. It knows
// nothing of a transaction begun here either: a BEGIN still waiting for the
// transaction's first statement is not sent by it, so what goes through it
// before that statement runs outside the transaction.
func (s *Conn) Wire() *wire.Conn { return s.conn }

// Close terminates the connection. It is idempotent.
func (s *Conn) Close(ctx context.Context) error { return s.conn.Close(ctx) }
