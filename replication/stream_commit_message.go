// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// StreamCommitMessage commits a streamed transaction.
type StreamCommitMessage struct {
	Xid               uint32
	Flags             uint8
	CommitLSN         LSN
	TransactionEndLSN LSN
	CommitTime        time.Time
}

func (*StreamCommitMessage) Type() byte { return msgStreamCommit }
