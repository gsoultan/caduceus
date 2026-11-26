// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// TruncateMessage is relations truncated together. Option is 1 for
// CASCADE, 2 for RESTART IDENTITY, both or neither.
type TruncateMessage struct {
	Xid         uint32
	Option      uint8
	RelationIDs []uint32
}

func (*TruncateMessage) Type() byte { return msgTruncate }
