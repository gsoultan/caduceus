// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"errors"

	"github.com/gsoultan/caduceus/codec"
	"github.com/gsoultan/caduceus/wire"
)

// copyChunk is how much row data is gathered before it goes out as CopyData.
const copyChunk = 64 << 10

// copySignature opens COPY's binary format: the signature, no flags, and no
// header extension.
const copySignature = "PGCOPY\n\377\r\n\x00\x00\x00\x00\x00\x00\x00\x00\x00"

// copyStream encodes the rows of one CopyFrom and sends them in chunks.
//
// It writes COPY's binary format, which the server reads without parsing, for
// as long as every value has a binary form. At the first that does not — a
// string for an integer column, which only the server's input function can
// read — it ends that COPY and continues in a second, in text format. Both run
// in one transaction, so they succeed or fail together.
type copyStream struct {
	session *Conn
	target  string
	oids    []uint32
	// own is a COPY outside any transaction, which runs in one of its own:
	// begun before the columns were described, committed behind the rows.
	own    bool
	binary bool

	buf     []byte
	mark    int
	scratch []byte
	written int64

	// statements is how many COPY statements have been begun, one or two,
	// and unsent how many of those are still in buf. Each one sent is answered
	// with a ReadyForQuery of its own.
	statements int
	unsent     int
}

// open starts the stream in binary if every column's type allows it. Nothing is
// sent yet: the COPY command travels with the first chunk of rows.
func (c *copyStream) open() {
	c.binary = binaryColumns(c.oids)
	c.buf = c.session.conn.Buffer()
	c.begin()
}

// binaryColumns reports whether every column type can be written in binary.
// COPY has one format for all its columns, so a single type that cannot makes
// the whole of it text.
func binaryColumns(oids []uint32) bool {
	for _, oid := range oids {
		if !codec.BinaryInput(oid) {
			return false
		}
	}
	return true
}

// begin appends the COPY command for the current format and starts the first
// CopyData message behind it.
func (c *copyStream) begin() {
	sql := "COPY " + c.target + " FROM STDIN"
	if c.binary {
		sql += " (FORMAT binary)"
	}
	c.statements, c.unsent = c.statements+1, c.unsent+1
	c.buf, c.mark = wire.StartMessage(wire.AppendQuery(c.buf, sql), 'd')
	if c.binary {
		c.buf = append(c.buf, copySignature...)
	}
}

// end closes the COPY in progress: the binary trailer, the CopyData message it
// belongs to, and CopyDone.
func (c *copyStream) end() {
	if c.binary {
		c.buf = wire.AppendInt16(c.buf, -1)
	}
	c.buf = wire.AppendCopyDone(finishCopyData(c.buf, c.mark))
}

// finishCopyData completes the CopyData message started at mark, or takes it
// back if nothing was written into it.
func finishCopyData(buf []byte, mark int) []byte {
	if len(buf)-mark == 4 {
		return buf[:mark-1]
	}
	return wire.FinishMessage(buf, mark)
}

// write encodes one row, changing to text if the row cannot be written in
// binary, and sends the chunk once it is full.
func (c *copyStream) write(values []any) error {
	if len(values) != len(c.oids) {
		return &ArgumentCountError{Want: len(c.oids), Got: len(values)}
	}
	if err := c.encode(values); err != nil {
		return err
	}
	c.written++
	if len(c.buf)-c.mark < copyChunk {
		return nil
	}
	if err := c.session.conn.Send(wire.FinishMessage(c.buf, c.mark)); err != nil {
		return err
	}
	c.unsent = 0
	c.buf, c.mark = wire.StartMessage(c.session.conn.Buffer(), 'd')
	return nil
}

func (c *copyStream) encode(values []any) error {
	if c.binary {
		row := len(c.buf)
		buf, err := appendCopyTuple(c.buf, c.oids, values)
		if !errors.Is(err, codec.ErrNoBinary) {
			c.buf = buf
			return err
		}
		c.buf = buf[:row]
		c.toText()
	}
	buf, scratch, err := appendCopyLine(c.buf, c.scratch, c.oids, values)
	c.buf, c.scratch = buf, scratch
	return err
}

// toText continues the stream in text format. A binary COPY that has taken rows
// is ended and a text one started behind it; one that has taken none was never
// sent, and is simply replaced.
func (c *copyStream) toText() {
	if c.written == 0 {
		c.buf, c.statements, c.unsent = c.buf[:0], 0, 0
	} else {
		c.end()
	}
	c.binary = false
	c.begin()
}

// finish ends the COPY and sends what is left, with the COMMIT of a
// transaction the COPY began for itself. The COMMIT is sent without waiting to
// see whether the COPY succeeded: in a transaction that has failed, the server
// treats it as the ROLLBACK that is wanted then.
func (c *copyStream) finish() error {
	c.end()
	if c.own {
		c.buf = wire.AppendQuery(c.buf, "COMMIT")
	}
	c.unsent = 0
	return c.session.conn.Send(c.buf)
}

// result reads the reply to every statement of a finished stream and returns
// how many rows the COPY took, summed over both parts of one that changed
// format. The first error is the one that matters: after it the server
// refuses a second COPY only because the first failed.
func (c *copyStream) result() (int64, error) {
	var total int64
	var failure error
	for owed := c.owed(); owed > 0; owed-- {
		tag, err := c.session.drain(nil)
		if err != nil && !isServerError(err) {
			return 0, err
		}
		if failure == nil {
			failure = err
		}
		total += copyCount(tag)
	}
	if failure != nil {
		return 0, failure
	}
	return total, nil
}

// owed is how many ReadyForQuery messages the server owes for what has been
// sent: one for each COPY statement, and one for the COMMIT or ROLLBACK that
// ends a transaction of the stream's own.
func (c *copyStream) owed() int {
	owed := c.statements - c.unsent
	if c.own {
		owed++
	}
	return owed
}

// abandon gives up a stream whose source failed, and returns why. What is
// still in the buffer is never sent. CopyFail aborts a COPY the server has
// started, and a transaction of the stream's own is rolled back, so nothing
// of the copy is kept; inside the caller's transaction the aborted COPY has
// failed that transaction, which is what undoes the rows there.
func (c *copyStream) abandon(reason error) error {
	buf := c.session.conn.Buffer()
	if c.statements > c.unsent {
		buf = wire.AppendCopyFail(buf, "caduceus: copy abandoned")
	}
	if c.own {
		buf = wire.AppendQuery(buf, "ROLLBACK")
	}
	owed := c.owed()
	if owed == 0 {
		return reason
	}
	if err := c.session.conn.Send(buf); err != nil {
		return err
	}
	for ; owed > 0; owed-- {
		if _, err := c.session.drain(nil); err != nil && !isServerError(err) {
			return err
		}
	}
	return reason
}

// appendCopyTuple appends one row of COPY binary format: the number of fields,
// then each as a length and its value, with a length of -1 for NULL.
func appendCopyTuple(buf []byte, oids []uint32, values []any) ([]byte, error) {
	buf = wire.AppendInt16(buf, int16(len(values)))
	for i, value := range values {
		length := len(buf)
		buf = append(buf, 0, 0, 0, 0)
		encoded, null, err := codec.EncodeBinary(buf, oids[i], value)
		if err != nil {
			return buf, &ArgumentError{Index: i, Err: err}
		}
		buf = encoded
		size := uint32(len(buf) - length - 4)
		if null {
			size = 0xFFFFFFFF
		}
		buf[length], buf[length+1], buf[length+2], buf[length+3] = byte(size>>24), byte(size>>16), byte(size>>8), byte(size)
	}
	return buf, nil
}

// appendCopyLine appends one row of COPY text format: tab-separated values,
// \N for NULL, with backslash, tab, newline and carriage return escaped.
func appendCopyLine(buf, scratch []byte, oids []uint32, values []any) ([]byte, []byte, error) {
	for i, value := range values {
		if i > 0 {
			buf = append(buf, '\t')
		}
		text, null, err := codec.EncodeText(scratch[:0], oids[i], value)
		if err != nil {
			return buf, scratch, &ArgumentError{Index: i, Err: err}
		}
		scratch = text
		if null {
			buf = append(buf, `\N`...)
			continue
		}
		buf = appendCopyEscaped(buf, text)
	}
	return append(buf, '\n'), scratch, nil
}

func appendCopyEscaped(buf, text []byte) []byte {
	for _, b := range text {
		switch b {
		case '\\':
			buf = append(buf, '\\', '\\')
		case '\t':
			buf = append(buf, '\\', 't')
		case '\n':
			buf = append(buf, '\\', 'n')
		case '\r':
			buf = append(buf, '\\', 'r')
		default:
			buf = append(buf, b)
		}
	}
	return buf
}
