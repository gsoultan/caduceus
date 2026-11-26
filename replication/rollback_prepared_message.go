// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// RollbackPreparedMessage is a prepared transaction rolled back: ROLLBACK
// PREPARED.
type RollbackPreparedMessage struct {
	Flags        uint8
	EndLSN       LSN
	RollbackLSN  LSN
	PrepareTime  time.Time
	RollbackTime time.Time
	Xid          uint32
	Gid          string
}

func (*RollbackPreparedMessage) Type() byte { return msgRollbackPrepared }
