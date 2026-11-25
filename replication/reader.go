// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import (
	"encoding/binary"
	"time"
)

// reader consumes a message body front to back. The first short read records
// ErrMalformed, and every read after it returns zero values, so a decoder can
// read a whole message and check once.
type reader struct {
	data []byte
	err  error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || n < 0 || len(r.data) < n {
		r.err = ErrMalformed
		return nil
	}
	taken := r.data[:n]
	r.data = r.data[n:]
	return taken
}

func (r *reader) byte() byte {
	if b := r.take(1); b != nil {
		return b[0]
	}
	return 0
}

func (r *reader) uint16() uint16 {
	if b := r.take(2); b != nil {
		return binary.BigEndian.Uint16(b)
	}
	return 0
}

func (r *reader) uint32() uint32 {
	if b := r.take(4); b != nil {
		return binary.BigEndian.Uint32(b)
	}
	return 0
}

func (r *reader) uint64() uint64 {
	if b := r.take(8); b != nil {
		return binary.BigEndian.Uint64(b)
	}
	return 0
}

// string reads a NUL-terminated string.
func (r *reader) string() string {
	if r.err != nil {
		return ""
	}
	for i, b := range r.data {
		if b == 0 {
			s := string(r.data[:i])
			r.data = r.data[i+1:]
			return s
		}
	}
	r.err = ErrMalformed
	return ""
}

// timestamp reads a server timestamp: microseconds since 2000-01-01 UTC.
func (r *reader) timestamp() time.Time {
	return postgresEpoch.Add(time.Duration(int64(r.uint64())) * time.Microsecond)
}

// peek returns the next byte without consuming it.
func (r *reader) peek() byte {
	if r.err != nil || len(r.data) == 0 {
		r.err = ErrMalformed
		return 0
	}
	return r.data[0]
}
