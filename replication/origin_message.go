// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// OriginMessage names the replication origin a transaction came from, on a
// server that applies changes from another.
type OriginMessage struct {
	CommitLSN LSN
	Name      string
}

func (*OriginMessage) Type() byte { return msgOrigin }
