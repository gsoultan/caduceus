// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/gsoultan/caduceus/wire"
)

// cancelTimeout bounds asking the server to stop a backup, which needs a
// connection of its own.
const cancelTimeout = 10 * time.Second

// backupStream reads what BASE_BACKUP sends: a result of the start
// position, one of the tablespaces, a CopyOut of the archives, and a result
// of the end position.
type backupStream struct {
	ctx     context.Context
	conn    *wire.Conn
	options BaseBackupOptions
	open    func(Archive) (io.WriteCloser, error)
	backup  Backup
	// result counts the row descriptions, so says which result a row is of.
	result int
	// archive is the one being written, and failure the first error: the
	// server's, the caller's or the stream's.
	archive io.WriteCloser
	failure error
}

func (s *backupStream) run(command string) error {
	if err := s.conn.Send(wire.AppendQuery(s.conn.Buffer(), command)); err != nil {
		return err
	}
	for {
		kind, body, err := s.conn.Receive()
		if err != nil {
			s.record(s.closeArchive())
			return errors.Join(s.failure, err)
		}
		if kind == wire.MsgReadyForQuery {
			s.record(s.closeArchive())
			return s.failure
		}
		s.handle(kind, body)
	}
}

func (s *backupStream) handle(kind byte, body []byte) {
	switch kind {
	case wire.MsgRowDescription:
		s.result++
	case wire.MsgDataRow:
		s.fail(s.row(body))
	case wire.MsgCopyData:
		s.fail(s.copyData(body))
	case wire.MsgCopyDone:
		s.record(s.closeArchive())
	case wire.MsgErrorResponse:
		s.failure = errors.Join(s.failure, wire.ParseError(body))
	}
}

// fail records the first error of the caller's or of the stream's, and asks
// the server to stop: a CopyOut cannot be stopped from the client's side
// otherwise, and reading a backup through only to discard it could take
// hours.
func (s *backupStream) fail(err error) {
	if err == nil || s.failure != nil {
		return
	}
	s.failure = err
	cancel, stop := context.WithTimeout(context.WithoutCancel(s.ctx), cancelTimeout)
	defer stop()
	_ = s.conn.CancelRequest(cancel)
}

// record keeps err as the backup's if it has none, without asking the
// server to stop: for the end, when it has already stopped.
func (s *backupStream) record(err error) {
	if s.failure == nil {
		s.failure = err
	}
}

func (s *backupStream) row(body []byte) error {
	columns, err := wire.SplitDataRow(nil, body)
	if err != nil {
		return err
	}
	switch s.result {
	case 0:
		s.backup.Start, s.backup.StartTimeline, err = position(columns)
	case 1:
		err = s.tablespace(columns)
	default:
		s.backup.End, s.backup.EndTimeline, err = position(columns)
	}
	return err
}

// position is a start or end row: an LSN and a timeline.
func position(columns [][]byte) (LSN, int32, error) {
	if len(columns) < 2 {
		return 0, 0, ErrMalformed
	}
	lsn, err := ParseLSN(string(columns[0]))
	if err != nil {
		return 0, 0, err
	}
	timeline, err := strconv.ParseInt(string(columns[1]), 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: timeline %q", ErrMalformed, columns[1])
	}
	return lsn, int32(timeline), nil
}

// tablespace is a row of spcoid, spclocation and size in kilobytes, the
// first two null for the main data directory and the size null without
// progress.
func (s *backupStream) tablespace(columns [][]byte) error {
	if len(columns) < 3 {
		return ErrMalformed
	}
	space := Tablespace{Location: string(columns[1]), Size: -1}
	if columns[0] != nil {
		oid, err := strconv.ParseUint(string(columns[0]), 10, 32)
		if err != nil {
			return fmt.Errorf("%w: tablespace %q", ErrMalformed, columns[0])
		}
		space.OID = uint32(oid)
	}
	if columns[2] != nil {
		kilobytes, err := strconv.ParseInt(string(columns[2]), 10, 64)
		if err != nil {
			return fmt.Errorf("%w: size %q", ErrMalformed, columns[2])
		}
		space.Size = kilobytes * 1024
	}
	s.backup.Tablespaces = append(s.backup.Tablespaces, space)
	return nil
}

// copyData is one message of the copy: n begins an archive, m the
// manifest, d is data of the one begun, and p reports progress.
func (s *backupStream) copyData(body []byte) error {
	if s.failure != nil {
		return nil
	}
	if len(body) == 0 {
		return ErrMalformed
	}
	switch body[0] {
	case 'n':
		return s.newArchive(body[1:])
	case 'm':
		return s.begin(Archive{Name: "backup_manifest", Manifest: true})
	case 'd':
		return s.write(body[1:])
	case 'p':
		return s.progress(body[1:])
	}
	return fmt.Errorf("%w: backup message %q", ErrMalformed, body[0])
}

func (s *backupStream) newArchive(body []byte) error {
	name, rest, ok := cut(body)
	if !ok {
		return ErrMalformed
	}
	tablespace, _, ok := cut(rest)
	if !ok || name == "" {
		return ErrMalformed
	}
	return s.begin(Archive{Name: name, Tablespace: tablespace})
}

func (s *backupStream) begin(archive Archive) error {
	if err := s.closeArchive(); err != nil {
		return err
	}
	if s.open == nil {
		return errors.New("caduceus/replication: the server sent an archive and BaseBackup was given no open function")
	}
	w, err := s.open(archive)
	if err != nil {
		return err
	}
	s.archive = w
	return nil
}

func (s *backupStream) write(data []byte) error {
	if s.archive == nil {
		return fmt.Errorf("%w: archive data before an archive", ErrMalformed)
	}
	_, err := s.archive.Write(data)
	return err
}

func (s *backupStream) progress(body []byte) error {
	if len(body) != 8 {
		return ErrMalformed
	}
	if s.options.Progress != nil {
		s.options.Progress(int64(binary.BigEndian.Uint64(body)))
	}
	return nil
}

// closeArchive closes the archive being written, if there is one.
func (s *backupStream) closeArchive() error {
	if s.archive == nil {
		return nil
	}
	err := s.archive.Close()
	s.archive = nil
	return err
}

// cut splits a NUL-terminated string off body.
func cut(body []byte) (string, []byte, bool) {
	for i, b := range body {
		if b == 0 {
			return string(body[:i]), body[i+1:], true
		}
	}
	return "", nil, false
}
