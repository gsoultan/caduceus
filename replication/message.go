// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// Message is one pgoutput message, as ParseMessage returns it: a
// *BeginMessage, *CommitMessage, *RelationMessage, *InsertMessage and so on.
// Type is its message type, the first byte of the record.
type Message interface {
	Type() byte
}
