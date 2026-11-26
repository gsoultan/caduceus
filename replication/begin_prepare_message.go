// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// BeginPrepareMessage begins a transaction prepared for two-phase commit,
// protocol version 3 and later with two_phase on.
type BeginPrepareMessage struct {
	PrepareLSN    LSN
	EndPrepareLSN LSN
	PrepareTime   time.Time
	Xid           uint32
	Gid           string
}

func (*BeginPrepareMessage) Type() byte { return msgBeginPrepare }
