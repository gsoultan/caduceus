// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"io"

	"github.com/gsoultan/caduceus/wire"
)

// errCopyDirection is a COPY that goes the other way from the call it was
// given to: TO STDOUT through CopyFromReader, or FROM STDIN through CopyTo.
var errCopyDirection = errors.New("caduceus: the COPY goes the other way: use CopyTo for TO STDOUT and CopyFromReader for FROM STDIN")

// CopyTo runs sql, a COPY ... TO STDOUT, and writes what the server sends to
// w as it arrives, in whatever format the COPY names. It returns the command
// tag, whose RowsAffected is the rows copied.
//
// A write to w that fails closes the connection, as pgx does: the server is
// still sending, and reading the rest only to drop it could take as long as
// the copy itself.
func (s *Conn) CopyTo(ctx context.Context, w io.Writer, sql string) (CommandTag, error) {
	if err := s.begin(ctx); err != nil {
		return "", err
	}
	tag, err := s.copyTo(ctx, w, sql)
	return CommandTag(tag), s.finish(err)
}

// CopyFromReader runs sql, a COPY ... FROM STDIN, and sends what r yields as
// its data, in whatever format the COPY names, until r is at its end. A read
// from r that fails ends the COPY with CopyFail, so nothing is copied, and
// returns that error. For rows of Go values, CopyFrom encodes them itself.
func (s *Conn) CopyFromReader(ctx context.Context, r io.Reader, sql string) (CommandTag, error) {
	if err := s.begin(ctx); err != nil {
		return "", err
	}
	tag, err := s.copyFromReader(r, sql)
	return CommandTag(tag), s.finish(err)
}

// copyTo runs a COPY TO STDOUT into w.
func (s *Conn) copyTo(ctx context.Context, w io.Writer, sql string) (string, error) {
	if err := s.startCopy(sql); err != nil {
		return "", err
	}
	var tag string
	var failure error
	for {
		kind, body, err := s.receive()
		if err != nil {
			return "", err
		}
		switch kind {
		case wire.MsgCopyData:
			if _, err := w.Write(body); err != nil {
				_ = s.conn.Close(context.WithoutCancel(ctx))
				return "", err
			}
		case wire.MsgCopyInResponse:
			failure = errors.Join(failure, errCopyDirection)
			if err := s.conn.Send(wire.AppendCopyFail(s.conn.Buffer(), errCopyDirection.Error())); err != nil {
				return "", err
			}
		case wire.MsgCommandComplete:
			tag = s.tag(body)
		case wire.MsgErrorResponse:
			failure = firstError(failure, wire.ParseError(body))
		case wire.MsgReadyForQuery:
			return tag, failure
		}
	}
}

// copyFromReader runs a COPY FROM STDIN from r.
func (s *Conn) copyFromReader(r io.Reader, sql string) (string, error) {
	if err := s.startCopy(sql); err != nil {
		return "", err
	}
	var tag string
	var failure error
	for {
		kind, body, err := s.receive()
		if err != nil {
			return "", err
		}
		switch kind {
		case wire.MsgCopyInResponse:
			readErr, err := s.sendFrom(r)
			if err != nil {
				return "", err
			}
			failure = firstError(failure, readErr)
		case wire.MsgCopyOutResponse:
			failure = errors.Join(failure, errCopyDirection)
		case wire.MsgCommandComplete:
			tag = s.tag(body)
		case wire.MsgErrorResponse:
			failure = firstError(failure, wire.ParseError(body))
		case wire.MsgReadyForQuery:
			return tag, failure
		}
	}
}

// startCopy sends a COPY over the simple protocol, behind the BEGIN of a
// transaction begun here, which is sent and answered first: the COPY's
// replies are read as they come, not after a Sync that would also answer it.
func (s *Conn) startCopy(sql string) error {
	if err := s.flushBegin(); err != nil {
		return err
	}
	return s.conn.Send(wire.AppendQuery(s.conn.Buffer(), sql))
}

// sendFrom sends what r yields as CopyData, then CopyDone, or CopyFail when a
// read fails. It returns that read's error, and separately an error sending,
// after which the connection is gone.
func (s *Conn) sendFrom(r io.Reader) (readErr, sendErr error) {
	chunk := make([]byte, copyChunk)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			if sendErr := s.conn.Send(wire.AppendCopyData(s.conn.Buffer(), chunk[:n])); sendErr != nil {
				return nil, sendErr
			}
		}
		switch {
		case errors.Is(err, io.EOF):
			return nil, s.conn.Send(wire.AppendCopyDone(s.conn.Buffer()))
		case err != nil:
			return err, s.conn.Send(wire.AppendCopyFail(s.conn.Buffer(), "caduceus: reading the data to copy failed"))
		}
	}
}

// firstError keeps the first of an exchange's errors.
func firstError(first, next error) error {
	if first != nil {
		return first
	}
	return next
}
