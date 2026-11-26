// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// StreamStartMessage begins a block of a large transaction streamed before
// it commits, protocol version 2 and later with streaming on. FirstSegment is
// the transaction's first block.
type StreamStartMessage struct {
	Xid          uint32
	FirstSegment bool
}

func (*StreamStartMessage) Type() byte { return msgStreamStart }
