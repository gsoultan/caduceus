// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// CommitMessage ends a transaction. TransactionEndLSN is just past its commit
// record, the position to confirm once the transaction is applied.
type CommitMessage struct {
	Flags             uint8
	CommitLSN         LSN
	TransactionEndLSN LSN
	CommitTime        time.Time
}

func (*CommitMessage) Type() byte { return msgCommit }
