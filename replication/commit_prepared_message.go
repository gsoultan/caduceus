// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// CommitPreparedMessage is a prepared transaction committed: COMMIT PREPARED.
type CommitPreparedMessage struct {
	Flags        uint8
	CommitLSN    LSN
	EndCommitLSN LSN
	CommitTime   time.Time
	Xid          uint32
	Gid          string
}

func (*CommitPreparedMessage) Type() byte { return msgCommitPrepared }
