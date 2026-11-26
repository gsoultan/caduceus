// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// UpdateMessage is a row updated. OldTupleType is TupleKey when OldTuple is
// the old row's replica identity, TupleOld when it is the whole old row,
// under REPLICA IDENTITY FULL, and zero when there is none: under the default
// identity, an update that did not change the key.
type UpdateMessage struct {
	Xid          uint32
	RelationID   uint32
	OldTupleType uint8
	OldTuple     *TupleData
	NewTuple     *TupleData
}

func (*UpdateMessage) Type() byte { return msgUpdate }
