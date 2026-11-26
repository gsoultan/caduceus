// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"errors"
	"io"
)

// largeObjectChunk bounds what one call reads or writes, so a large Read or
// Write is a series of statements of a megabyte rather than one statement
// that holds the whole of it in memory twice, on both ends.
const largeObjectChunk = 1 << 20

// LargeObject is an open large object: an io.Reader, io.Writer, io.Seeker
// and io.Closer whose every call is a statement in the transaction it was
// opened in, run in the context it was opened with.
type LargeObject struct {
	ctx    context.Context
	tx     *Tx
	fd     int32
	closed bool
}

var _ io.ReadWriteSeeker = (*LargeObject)(nil)

// Read reads up to len(p) bytes from the current position. At the end of the
// object it returns io.EOF.
func (o *LargeObject) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		var chunk []byte
		if err := o.tx.QueryRow(o.ctx, "SELECT loread($1, $2)", o.fd, int32(min(len(p)-n, largeObjectChunk))).Scan(&chunk); err != nil {
			return n, err
		}
		n += copy(p[n:], chunk)
		if len(chunk) == 0 {
			return n, eofIfEmpty(n)
		}
	}
	return n, nil
}

// eofIfEmpty is io.EOF for a read that found nothing.
func eofIfEmpty(n int) error {
	if n == 0 {
		return io.EOF
	}
	return nil
}

// Write writes p at the current position.
func (o *LargeObject) Write(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		piece := p[n:min(len(p), n+largeObjectChunk)]
		var written int32
		if err := o.tx.QueryRow(o.ctx, "SELECT lowrite($1, $2)", o.fd, piece).Scan(&written); err != nil {
			return n, err
		}
		n += int(written)
		if int(written) < len(piece) {
			return n, io.ErrShortWrite
		}
	}
	return n, nil
}

// Seek moves the current position, whence being io.SeekStart, io.SeekCurrent
// or io.SeekEnd, and returns the new one.
func (o *LargeObject) Seek(offset int64, whence int) (int64, error) {
	if whence < io.SeekStart || whence > io.SeekEnd {
		return 0, errors.New("caduceus: Seek with an unknown whence")
	}
	var position int64
	err := o.tx.QueryRow(o.ctx, "SELECT lo_lseek64($1, $2, $3)", o.fd, offset, int32(whence)).Scan(&position)
	return position, err
}

// Tell returns the current position.
func (o *LargeObject) Tell() (int64, error) {
	var position int64
	err := o.tx.QueryRow(o.ctx, "SELECT lo_tell64($1)", o.fd).Scan(&position)
	return position, err
}

// Truncate cuts or extends the object to size bytes.
func (o *LargeObject) Truncate(size int64) error {
	var done int32
	return o.tx.QueryRow(o.ctx, "SELECT lo_truncate64($1, $2)", o.fd, size).Scan(&done)
}

// Close closes the object. The transaction's end closes it too. Closing it
// again does nothing: a second lo_close would be an error, and an error fails
// the whole transaction.
func (o *LargeObject) Close() error {
	if o.closed {
		return nil
	}
	o.closed = true
	var done int32
	return o.tx.QueryRow(o.ctx, "SELECT lo_close($1)", o.fd).Scan(&done)
}
