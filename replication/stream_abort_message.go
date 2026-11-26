// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// StreamAbortMessage aborts a streamed transaction, or one of its
// subtransactions when SubXid differs from Xid. AbortLSN and AbortTime are
// sent by protocol version 4 with parallel streaming, and are zero otherwise.
type StreamAbortMessage struct {
	Xid       uint32
	SubXid    uint32
	AbortLSN  LSN
	AbortTime time.Time
}

func (*StreamAbortMessage) Type() byte { return msgStreamAbort }
