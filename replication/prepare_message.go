// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// PrepareMessage is a transaction prepared: PREPARE TRANSACTION. A
// StreamPrepare is one too, of a transaction that was streamed, with Streamed
// set.
type PrepareMessage struct {
	Flags         uint8
	PrepareLSN    LSN
	EndPrepareLSN LSN
	PrepareTime   time.Time
	Xid           uint32
	Gid           string
	Streamed      bool
}

func (m *PrepareMessage) Type() byte {
	if m.Streamed {
		return msgStreamPrepare
	}
	return msgPrepare
}
