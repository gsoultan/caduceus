// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// LogicalDecodingMessage is what pg_logical_emit_message wrote, sent only
// with the messages option. A transactional one is inside its transaction;
// another arrives on its own.
type LogicalDecodingMessage struct {
	Xid           uint32
	LSN           LSN
	Transactional bool
	Prefix        string
	Content       []byte
}

func (*LogicalDecodingMessage) Type() byte { return msgMessage }
