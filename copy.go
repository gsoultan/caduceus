// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/gsoultan/caduceus/wire"
)

// copyFrom streams rows into a table with COPY.
//
// It takes two round trips whatever the number of rows. The first learns the
// column types, from a Parse and Describe of a SELECT over the columns. The
// second carries the COPY command, the rows and the Sync that ends it; the rows
// are encoded for the described types as they are pulled from the source.
func (s *Conn) copyFrom(ctx context.Context, request copyRequest) (int64, error) {
	if request.Rows == nil {
		return 0, ErrNoCopySource
	}
	if err := s.begin(ctx); err != nil {
		return 0, err
	}
	copied, err := s.copyRows(request)
	return copied, s.finish(err)
}

func (s *Conn) copyRows(request copyRequest) (int64, error) {
	table := quoteQualifiedName(request.Table)
	columns := make([]string, len(request.Columns))
	for i, column := range request.Columns {
		columns[i] = quoteIdentifier(column)
	}
	list := strings.Join(columns, ", ")

	oids, err := s.describeColumns("SELECT " + list + " FROM " + table)
	if err != nil {
		return 0, err
	}
	stream := copyStream{session: s, target: table + " (" + list + ")", oids: oids}
	stream.open()
	if err := sendRows(&stream, request.Rows); err != nil {
		return 0, s.abandonCopy(err)
	}
	if err := stream.finish(); err != nil {
		return 0, err
	}
	return s.copied()
}

// describeColumns returns the types of a query's columns.
//
// It ends with a Flush, not a Sync, which leaves the implicit transaction open
// and with it the lock Parse took on the table. The types described are then
// the types written to: a column cannot be altered between this and the COPY,
// which matters once values are sent in binary, where four bytes meant as an
// int4 would be read as whatever the column had become. The caller owes the
// server a Sync.
func (s *Conn) describeColumns(sql string) ([]uint32, error) {
	buf := s.closeEvicted(s.conn.Buffer())
	buf = wire.AppendParse(buf, "", sql, nil)
	buf = wire.AppendDescribe(buf, 'S', "")
	if err := s.conn.Send(wire.AppendFlush(buf)); err != nil {
		return nil, err
	}
	for {
		kind, body, err := s.conn.Receive()
		if err != nil {
			return nil, err
		}
		switch kind {
		case wire.MsgRowDescription:
			return s.columnTypes(body)
		case wire.MsgErrorResponse:
			return nil, s.settle(wire.ParseError(body))
		}
	}
}

func (s *Conn) columnTypes(description []byte) ([]uint32, error) {
	fields, err := wire.ParseRowDescription(nil, description)
	if err != nil {
		return nil, s.settle(err)
	}
	oids := make([]uint32, len(fields))
	for i, field := range fields {
		oids[i] = field.DataTypeOID
	}
	return oids, nil
}

// sendRows writes every row of the source to the stream.
func sendRows(stream *copyStream, rows CopySource) error {
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return err
		}
		if err := stream.write(values); err != nil {
			return err
		}
	}
	return rows.Err()
}

// copied reads the replies to a COPY through to ReadyForQuery and returns how
// many rows it took, summed over both parts of one that changed format.
func (s *Conn) copied() (int64, error) {
	var total int64
	var failure error
	for {
		kind, body, err := s.conn.Receive()
		if err != nil {
			return 0, err
		}
		switch kind {
		case wire.MsgCommandComplete:
			total += copyCount(s.tag(body))
		case wire.MsgErrorResponse:
			if failure == nil {
				failure = wire.ParseError(body)
			}
		case wire.MsgReadyForQuery:
			if failure != nil {
				return 0, failure
			}
			return total, nil
		}
	}
}

// abandonCopy gives up a COPY whose source failed and returns why. CopyFail
// aborts a COPY the server has started, and is ignored by one that has not
// heard of it yet; the Sync ends the transaction either way.
func (s *Conn) abandonCopy(reason error) error {
	buf := wire.AppendCopyFail(s.conn.Buffer(), "caduceus: copy abandoned")
	if err := s.conn.Send(wire.AppendSync(buf)); err != nil {
		return err
	}
	if _, err := s.drain(nil); err != nil && !isServerError(err) {
		return err
	}
	return reason
}

// settle sends the Sync a failed exchange still owes, reads through to
// ReadyForQuery, and returns the failure.
func (s *Conn) settle(failure error) error {
	if err := s.conn.Send(wire.AppendSync(s.conn.Buffer())); err != nil {
		return err
	}
	if _, err := s.drain(nil); err != nil && !isServerError(err) {
		return err
	}
	return failure
}

// copyCount reads the row count from a "COPY n" tag.
func copyCount(tag string) int64 {
	n, _ := strconv.ParseInt(strings.TrimPrefix(tag, "COPY "), 10, 64)
	return n
}

func isServerError(err error) bool {
	var failure *wire.Error
	return errors.As(err, &failure)
}

// quoteQualifiedName quotes each part of a possibly schema-qualified name.
func quoteQualifiedName(parts []string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = quoteIdentifier(part)
	}
	return strings.Join(quoted, ".")
}
