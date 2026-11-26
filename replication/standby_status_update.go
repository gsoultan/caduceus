// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// StandbyStatusUpdate is a Standby Status Update: how far the client has
// written, flushed and applied the stream. The server may drop WAL up to
// the flushed position, and a logical slot confirms it. ClientTime zero is
// now. ReplyRequested asks for a keepalive back.
type StandbyStatusUpdate struct {
	WALWritePosition LSN
	WALFlushPosition LSN
	WALApplyPosition LSN
	ClientTime       time.Time
	ReplyRequested   bool
}
