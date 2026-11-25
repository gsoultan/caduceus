// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
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

	values [][]byte
	tag    string
	err    error
	// ended is set once the statement's result is over; settled once the whole
	// exchange is, which for a lone statement is the same moment.
	ended   bool
	settled bool
	closed  bool

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

// Next advances to the next row.
func (c *Rows) Next() bool {
	c.values = c.values[:0]
	if c.ended || c.closed {
		return false
	}
	for {
		kind, body, err := c.session.conn.Receive()
		if err != nil {
			c.fail(err)
			return false
		}
		switch kind {
		case wire.MsgDataRow:
			if c.values, err = wire.SplitDataRow(c.values[:0], body); err != nil || len(c.values) != len(c.stmt.fields) {
				c.fail(wire.ErrProtocol)
				return false
			}
			return true
		case wire.MsgCommandComplete:
			c.tag = c.session.tag(body)
			if c.inBatch {
				c.ended = true
				return false
			}
		case wire.MsgEmptyQueryResponse:
			if c.inBatch {
				c.ended = true
				return false
			}
		case wire.MsgErrorResponse:
			if c.err == nil {
				c.err = c.session.serverError(c.stmt, body)
			}
			if c.inBatch {
				// The server skips everything up to the batch's Sync, so the
				// statements after this one have no replies of their own.
				c.ended = true
				return false
			}
		case wire.MsgReadyForQuery:
			c.settle(nil)
			return false
		}
	}
}

// settle ends the exchange when the cursor saw its ReadyForQuery.
func (c *Rows) settle(err error) {
	c.ended, c.settled = true, true
	if finished := c.session.finish(err); finished != nil && c.err == nil {
		c.err = finished
	}
}

func (c *Rows) fail(err error) {
	if c.err == nil {
		c.err = err
	}
	c.settle(err)
}

// Scan decodes the current row into dest.
func (c *Rows) Scan(dest ...any) error {
	if len(c.values) == 0 && len(c.stmt.fields) > 0 {
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
			return fmt.Errorf("caduceus: column %q: %w", field.Name, err)
		}
	}
	return nil
}

// Err returns the error that ended the result, if any.
func (c *Rows) Err() error { return c.err }

// Close reads whatever is left of the result, so the connection is back at a
// boundary. It is idempotent.
func (c *Rows) Close() {
	if c.closed {
		return
	}
	for c.Next() {
	}
	c.closed = true
}

// Fields describes the result's columns, with the format each arrived in.
func (c *Rows) Fields() []wire.Field { return c.stmt.fields }

// RawValues returns the current row's undecoded values.
func (c *Rows) RawValues() [][]byte { return c.values }

// Tag returns the command tag, available once the result is over.
func (c *Rows) Tag() string { return c.tag }
