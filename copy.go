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
// second carries the COPY command, the rows, and what ends them; the rows are
// encoded for the described types as they are pulled from the source.
//
// Outside a transaction it runs in one of its own, begun before the describe
// and committed behind the rows. That does two things. The lock Parse takes on
// the table is held until the COPY has run, so the types described are the
// types written to: a column cannot be altered in between, which matters once
// values are sent in binary, where four bytes meant as an int4 would be read
// as whatever the column had become. And a COPY that changes format part way,
// which is two COPY statements, is still all or nothing. Inside a transaction
// the caller's does both.
//
// COPY itself is sent as a simple Query, as libpq and every other driver send
// it. The extended protocol would save the transaction, but poolers have had
// defects following a COPY through it.
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

	own := s.conn.TxStatus() == wire.TxIdle
	oids, err := s.describeColumns("SELECT "+list+" FROM "+table, own)
	if err != nil {
		return 0, err
	}
	stream := copyStream{session: s, target: table + " (" + list + ")", oids: oids, own: own}
	stream.open()
	if err := sendRows(&stream, request.Rows); err != nil {
		return 0, stream.abandon(err)
	}
	if err := stream.finish(); err != nil {
		return 0, err
	}
	return stream.result()
}

// describeColumns returns the types of a query's columns, first beginning a
// transaction if own is set. If the describe fails, that transaction is rolled
// back before the failure is returned.
func (s *Conn) describeColumns(sql string, own bool) ([]uint32, error) {
	buf := s.conn.Buffer()
	if own {
		buf = wire.AppendQuery(buf, "BEGIN")
	}
	buf = s.closeEvicted(buf)
	buf = wire.AppendParse(buf, "", sql, nil)
	buf = wire.AppendDescribe(buf, 'S', "")
	if err := s.conn.Send(wire.AppendSync(buf)); err != nil {
		return nil, err
	}
	var began error
	if own {
		if _, began = s.drain(nil); began != nil && !isServerError(began) {
			return nil, began
		}
	}
	described, err := s.readDescription("", "")
	if err == nil && began == nil {
		return columnTypes(described), nil
	}
	if err != nil && !isServerError(err) {
		return nil, err
	}
	if own {
		if rollback := s.rollbackOwn(); rollback != nil {
			return nil, rollback
		}
	}
	return nil, errors.Join(began, err)
}

func columnTypes(described *statement) []uint32 {
	oids := make([]uint32, len(described.fields))
	for i, field := range described.fields {
		oids[i] = field.DataTypeOID
	}
	return oids
}

// rollbackOwn ends the transaction a COPY began for itself. It returns only a
// failure of the connection; a server's complaint about the ROLLBACK changes
// nothing about what the caller is told.
func (s *Conn) rollbackOwn() error {
	if err := s.conn.Send(wire.AppendQuery(s.conn.Buffer(), "ROLLBACK")); err != nil {
		return err
	}
	if _, err := s.drain(nil); err != nil && !isServerError(err) {
		return err
	}
	return nil
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
