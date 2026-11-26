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
	// stmts holds a statement for each of the count queries, or one for all
	// of them when they share their text.
	stmts []*statement
	count int

	// pending is a batch too large to send at once, and windows the parts it
	// goes in, of which sent have gone and written bytes with them. A batch
	// that went in one write has none of these.
	pending []byte
	windows []batchWindow
	sent    int
	written int

	next    int
	current *Rows
	failure error
	settled bool
}

// sendWindow writes the next window of a batch sent in parts.
func (b *BatchResults) sendWindow() error {
	window := b.windows[b.sent]
	err := b.session.conn.Write(b.pending[b.written:window.end])
	b.sent, b.written = b.sent+1, window.end
	if b.sent == len(b.windows) {
		b.pending = nil
	}
	return err
}

// unsent reports whether part of the batch has yet to be sent.
func (b *BatchResults) unsent() bool { return b.sent < len(b.windows) }

// due reports whether the next window is to be sent before the statement at
// next is read. Reading the first statement of the last window sent is the
// moment, which keeps the server supplied and never has more than two windows
// unread by it. A window that begins with a statement too large to share one
// waits until every statement before it has been read.
func (b *BatchResults) due() bool {
	if !b.unsent() {
		return false
	}
	if window := b.windows[b.sent]; window.alone {
		return b.next >= window.first
	}
	return b.next >= b.windows[b.sent-1].first
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
	if b.failure != nil && !b.settled {
		// The server ran nothing after the statement that failed, so the rest
		// have no result to read: they report the failure too.
		b.close()
	}
	if b.settled {
		if b.failure != nil {
			return b.failure
		}
		return errBatchExhausted
	}
	if b.next >= b.count {
		return errBatchExhausted
	}
	if b.due() {
		if err := b.sendWindow(); err != nil {
			b.settled = true
			return b.remember(b.session.finish(err))
		}
	}
	*rows = Rows{}
	rows.init(b.session, statementAt(b.stmts, b.next), true)
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
	// The server described the statement differently from how it was
	// remembered. The queries after this one that share its Parse have no
	// description of their own to learn that from.
	restate(b.stmts, statementAt(b.stmts, b.next-1), c.stmt)
}

// close reads every remaining reply up to the batch's ReadyForQuery and
// returns the batch's first error.
func (b *BatchResults) close() error {
	b.finishCurrent()
	b.readRest()
	if b.settled {
		return b.failure
	}
	if b.failure != nil {
		b.abandon()
	}
	for {
		kind, body, err := b.session.receive()
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

// readRest reads the result of every statement not read yet, which sends the
// windows behind them, until one has failed. The server runs nothing after a
// failure, up to the Sync, so all that is left then is the error and the
// ReadyForQuery, which close reads.
//
// Read as results, a statement's columns are still held to what was
// remembered of it, and its errors are still told apart. Read as replies
// alone, as the last window of a batch used to be, a batch closed without its
// results read never dropped a description the server said no longer held,
// and failed again on every attempt.
func (b *BatchResults) readRest() {
	if !b.unread() {
		return
	}
	rows := new(Rows)
	for b.unread() {
		if b.statementInto(rows) != nil {
			return
		}
		b.finishCurrent()
	}
}

// unread reports whether a statement's result is still to be read.
func (b *BatchResults) unread() bool {
	return b.next < b.count && b.failure == nil && !b.settled
}

// abandon ends a batch a statement of which failed. The server is skipping
// everything up to a Sync, so if the batch's own has not been sent, in its
// last window, one is sent in its place and the rest never goes.
func (b *BatchResults) abandon() {
	if !b.unsent() {
		return
	}
	b.sent, b.pending = len(b.windows), nil
	var sync [5]byte
	if err := b.session.conn.Write(wire.AppendSync(sync[:0])); err != nil {
		b.settled = true
		b.remember(b.session.finish(err))
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
func (b *BatchResults) Exec() (CommandTag, error) {
	rows, err := b.statement()
	if err != nil {
		return "", err
	}
	rows.Close()
	return CommandTag(rows.tag), rows.err
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
