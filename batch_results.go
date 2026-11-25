// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"errors"

	"github.com/gsoultan/caduceus/wire"
)

// errBatchExhausted is returned when more results are read than were queued.
var errBatchExhausted = errors.New("caduceus: no more results in the batch")

// BatchResults reads a pipelined batch's replies off the connection, one
// statement at a time, in the order they were queued.
type BatchResults struct {
	session *Conn
	stmts   []*statement
	next    int
	current *Rows
	failure error
	settled bool
}

// statement returns a cursor over the next statement's result, closing the
// previous one first so the connection is positioned at its start.
func (b *BatchResults) statement() (*Rows, error) {
	rows := new(Rows)
	if err := b.statementInto(rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// statementInto is statement into rows the caller supplies.
func (b *BatchResults) statementInto(rows *Rows) error {
	b.finishCurrent()
	if b.settled {
		if b.failure != nil {
			return b.failure
		}
		return errBatchExhausted
	}
	if b.next >= len(b.stmts) {
		return errBatchExhausted
	}
	rows.init(b.session, b.stmts[b.next], true)
	b.current = rows
	b.next++
	return nil
}

// finishCurrent reads what is left of the current statement.
func (b *BatchResults) finishCurrent() {
	if b.current == nil {
		return
	}
	b.current.Close()
	b.note(b.current)
	b.current = nil
}

func (b *BatchResults) note(c *Rows) {
	if c.err != nil && b.failure == nil {
		b.failure = c.err
	}
	if c.settled {
		b.settled = true
	}
}

// close reads every remaining reply up to the batch's ReadyForQuery and
// returns the batch's first error.
func (b *BatchResults) close() error {
	b.finishCurrent()
	if b.settled {
		return b.failure
	}
	for {
		kind, body, err := b.session.conn.Receive()
		if err != nil {
			b.settled = true
			return b.remember(b.session.finish(err))
		}
		switch kind {
		case wire.MsgErrorResponse:
			b.remember(wire.ParseError(body))
		case wire.MsgReadyForQuery:
			b.settled = true
			return b.remember(b.session.finish(nil))
		}
	}
}

func (b *BatchResults) remember(err error) error {
	if err != nil && b.failure == nil {
		b.failure = err
	}
	return b.failure
}

// Exec reads the next statement's result, discarding any rows, and returns
// its command tag.
func (b *BatchResults) Exec() (string, error) {
	rows, err := b.statement()
	if err != nil {
		return "", err
	}
	rows.Close()
	return rows.tag, rows.err
}

// Query returns the next statement's rows. Reading the next result, or
// closing the batch, closes them.
func (b *BatchResults) Query() (*Rows, error) {
	return b.statement()
}

// QueryRow returns the next statement's result as a single row.
func (b *BatchResults) QueryRow() *Row {
	row := new(Row)
	row.err = b.statementInto(&row.rows)
	return row
}

// Close reads every remaining result and returns the batch's first error.
// It is idempotent, and the connection is free once it returns.
func (b *BatchResults) Close() error { return b.close() }
