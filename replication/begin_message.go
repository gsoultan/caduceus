// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// BeginMessage begins a transaction: FinalLSN is where its commit record is.
type BeginMessage struct {
	FinalLSN   LSN
	CommitTime time.Time
	Xid        uint32
}

func (*BeginMessage) Type() byte { return msgBegin }
