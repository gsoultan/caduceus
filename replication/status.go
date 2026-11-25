// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import (
	"encoding/binary"
	"time"
)

// AppendStandbyStatus appends a standby status update to dst: the body of a
// CopyData message, to be wrapped with wire.AppendCopyData.
//
// written is how far the client has received; flushed and applied how far it
// has durably handled. The server keeps the slot's WAL from flushed onwards, so
// a flushed position that never advances fills the primary's disk.
func AppendStandbyStatus(dst []byte, written, flushed, applied LSN, now time.Time, replyRequested bool) []byte {
	var update [34]byte
	update[0] = StandbyStatus
	binary.BigEndian.PutUint64(update[1:], uint64(written))
	binary.BigEndian.PutUint64(update[9:], uint64(flushed))
	binary.BigEndian.PutUint64(update[17:], uint64(applied))
	binary.BigEndian.PutUint64(update[25:], uint64(now.Sub(postgresEpoch).Microseconds()))
	if replyRequested {
		update[33] = 1
	}
	return append(dst, update[:]...)
}
