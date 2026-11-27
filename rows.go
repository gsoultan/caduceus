// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"errors"
	"fmt"

	"github.com/gsoultan/caduceus/codec"
	"github.com/gsoultan/caduceus/wire"
)

// Rows reads one statement's result off the connection as it is iterated.
//
// Rows are never buffered: each Next reads one DataRow, and its values alias
// the connection's read buffer until the next call. In a batch the cursor stops
// at the end of its own statement and leaves the exchange open for the next.
type Rows struct {
	session *Conn
	stmt    *statement
	inBatch bool
	// ownSync is a statement of a pipeline, which has a Sync of its own: its
	// result runs to a ReadyForQuery, which ends it without ending the
	// exchange, and an error in it leaves the statements after it unaffected.
	ownSync bool

	values [][]byte
	tag    string
	err    error
	// ended is set once the statement's result is over; settled once the whole
	// exchange is, which for a lone statement is the same moment.
	ended   bool
	settled bool
	closed  bool
	// started is set once anything of the result has been read, and ahead
	// while values hold a row that Fields read before Next asked for it.
	// bound is the statement's BindComplete read: an error from then on is
	// the statement's own failure, and says nothing of what is remembered of
	// it.
	started bool
	ahead   bool
	bound   bool

	// inline holds the values of a narrow row, saving their slice an
	// allocation of its own.
	inline [6][]byte
}

func (c *Rows) init(s *Conn, stmt *statement, inBatch bool) {
	c.session, c.stmt, c.inBatch = s, stmt, inBatch
	if n := len(stmt.fields); n <= len(c.inline) {
		c.values = c.inline[:0:n]
	} else {
		c.values = make([][]byte, 0, n)
	}
}

// A read of the result stops at the next row, at the result's description,
// or only at its end, with every row on the way skipped undecoded.
type readTo uint8

const (
	toRow readTo = iota
	toDescription
	toEnd
)

// Next advances to the next row.
func (c *Rows) Next() bool {
	if c.ahead {
		c.ahead = false
		return true
	}
	c.values = c.values[:0]
	if c.ended || c.closed {
		return false
	}
	return c.read(toRow)
}

// read reads the result as far as stop says, or to its end, and reports
// whether it stopped at a row.
func (c *Rows) read(stop readTo) bool {
	c.started = true
	for {
		kind, body, err := c.session.receive()
		if err != nil {
			c.fail(err)
			return false
		}
		switch kind {
		case wire.MsgDataRow:
			if stop != toEnd {
				return c.row(body)
			}
		case wire.MsgRowDescription, wire.MsgNoData:
			// A statement that is not kept on the server was parsed again for
			// this result, and this is what the server makes of it now.
			if !describes(c.stmt, kind, body) && !c.redescribe(kind, body) {
				return false
			}
			if stop == toDescription {
				return false
			}
		case wire.MsgBindComplete:
			c.bound = true
		case wire.MsgCommandComplete:
			c.tag = c.session.tag(body)
			if c.over() {
				return false
			}
		case wire.MsgEmptyQueryResponse:
			if c.over() {
				return false
			}
		case wire.MsgErrorResponse:
			if c.err == nil {
				c.err = c.session.serverError(c.stmt, body, c.bound)
			}
			// In a batch the server skips everything up to the batch's Sync,
			// so the statements after this one have no replies of their own.
			if c.over() {
				return false
			}
		case wire.MsgReadyForQuery:
			if c.ownSync {
				c.ended = true
				return false
			}
			c.settle(nil)
			return false
		}
	}
}

// row takes a DataRow as the current row, and reports whether it is one of
// this result's. One that is not, malformed, or with another number of
// columns than the statement was described with, as when a pooler has
// prepared it again where a table has changed, means the stream no longer
// says what it should, and the connection is closed.
func (c *Rows) row(body []byte) bool {
	var err error
	if c.values, err = wire.SplitDataRow(c.values[:0], body); err == nil && len(c.values) == len(c.stmt.fields) {
		return true
	}
	c.values = c.values[:0]
	c.fail(c.session.broken())
	return false
}

// over ends a statement of a batch at its last reply, a CommandComplete or an
// error, and reports whether it did. A lone statement, and one of a pipeline,
// which has a Sync of its own, run on to ReadyForQuery.
func (c *Rows) over() bool {
	c.ended = c.inBatch && !c.ownSync
	return c.ended
}

// redescribe takes up the columns the server has just described, which are not
// the ones remembered: a table was altered since, or another schema is first
// on the search path. The result was asked for in the formats that suited the
// columns remembered, so it is read as those formats of the types it turns out
// to have, and a column whose type cannot be read in the format it comes in is
// refused when it is scanned, with ErrResultChanged. What was remembered is
// dropped, so the next execution asks afresh and gets formats that suit.
func (c *Rows) redescribe(kind byte, body []byte) bool {
	var fields []wire.Field
	if kind == wire.MsgRowDescription {
		var err error
		if fields, err = wire.ParseRowDescription(nil, body); err != nil {
			c.fail(err)
			return false
		}
	}
	c.session.cache.remove(c.stmt)
	c.stmt = c.stmt.restated(fields)
	return true
}

// settle ends the exchange when the cursor saw its ReadyForQuery.
func (c *Rows) settle(err error) {
	c.ended, c.settled = true, true
	if finished := c.session.finish(err); finished != nil && c.err == nil {
		c.err = finished
	}
	c.session.traceRowsEnd(c)
}

// fail ends the exchange on an error of the connection. What is kept is what
// finish makes of it: for a read cut short by the context, an error that says
// so, where the read's own says only that it timed out.
func (c *Rows) fail(err error) {
	c.settle(err)
}

// Scan decodes the current row into dest.
func (c *Rows) Scan(dest ...any) error {
	if !c.onRow() {
		return ErrNotOnARow
	}
	if len(dest) != len(c.values) {
		return fmt.Errorf("caduceus: %d columns in the result, %d destinations", len(c.values), len(dest))
	}
	for i, value := range c.values {
		field := &c.stmt.fields[i]
		if dest[i] == nil {
			continue
		}
		if err := codec.Scan(field.DataTypeOID, field.Format, value, dest[i]); err != nil {
			return columnError(field, c.session.binary[field.DataTypeOID], err)
		}
	}
	return nil
}

// onRow reports whether Next has moved onto a row that Scan and Values can
// read: one Fields read ahead is not yet Next's.
func (c *Rows) onRow() bool {
	return !c.ahead && (len(c.values) > 0 || len(c.stmt.fields) == 0)
}

// columnError says which column could not be scanned, and, for one that
// arrived in a format chosen for the type it had when the statement was
// described, that the statement's result changed since then: the statement
// ran, and its column is not one that can be read in that format.
//
// A column of a type Options.BinaryResults asks for in binary was asked for in
// the format its type gets, so its result has not changed for being unreadable.
func columnError(field *wire.Field, binary bool, err error) error {
	var unreadable *codec.TypeError
	if !binary && field.Format != codec.ResultFormat(field.DataTypeOID) && errors.As(err, &unreadable) {
		return fmt.Errorf("caduceus: column %q: %w: %w", field.Name, ErrResultChanged, err)
	}
	return fmt.Errorf("caduceus: column %q: %w", field.Name, err)
}

// Values decodes every column of the current row into the Go value that fits
// it best, as Scan does into a *any: int16, int32, int64, float64, string,
// []byte, time.Time and so on, and nil for NULL. The slice and everything in
// it are allocated afresh by each call, so they stay valid after Next.
func (c *Rows) Values() ([]any, error) {
	if !c.onRow() {
		return nil, ErrNotOnARow
	}
	values := make([]any, len(c.values))
	for i, value := range c.values {
		field := &c.stmt.fields[i]
		natural, err := codec.Natural(field.DataTypeOID, field.Format, value)
		if err != nil {
			return nil, columnError(field, c.session.binary[field.DataTypeOID], err)
		}
		values[i] = natural
	}
	return values, nil
}

// Err returns the error that ended the result, if any.
func (c *Rows) Err() error { return c.err }

// Close reads whatever is left of the result, skipping its rows undecoded, so
// the connection is back at a boundary. It is idempotent.
func (c *Rows) Close() {
	if c.closed {
		return
	}
	c.ahead, c.values = false, c.values[:0]
	if !c.ended {
		c.read(toEnd)
	}
	c.closed = true
}

// Fields describes the result's columns, with the format each arrived in.
//
// On a connection that keeps no statements on the server, the server describes
// a statement afresh in front of its rows, and the columns are the ones it
// describes, which differ from those remembered if the statement has come to
// mean something else. Asked for before the first Next, Fields reads as far as
// that description, which is already on its way and costs no round trip, and
// keeps any row it finds behind it for Next.
func (c *Rows) Fields() []wire.Field {
	if c.unread() {
		c.ahead = c.read(toDescription)
	}
	return c.stmt.fields
}

// unread reports whether nothing of the result has been read yet, of a
// statement that the server describes again in front of its rows.
func (c *Rows) unread() bool {
	return !c.started && !c.ended && !c.closed && c.stmt.name == ""
}

// RawValues returns the current row's undecoded values.
func (c *Rows) RawValues() [][]byte {
	if c.ahead {
		return nil
	}
	return c.values
}

// Tag returns the command tag, available once the result is over.
func (c *Rows) Tag() CommandTag { return CommandTag(c.tag) }
