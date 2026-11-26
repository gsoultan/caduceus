// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// StreamStopMessage ends a block of a streamed transaction.
type StreamStopMessage struct{}

func (*StreamStopMessage) Type() byte { return msgStreamStop }
