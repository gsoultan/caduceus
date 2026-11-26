// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// DeleteMessage is a row deleted: OldTuple is its replica identity
// (OldTupleType TupleKey) or the whole row (TupleOld).
type DeleteMessage struct {
	Xid          uint32
	RelationID   uint32
	OldTupleType uint8
	OldTuple     *TupleData
}

func (*DeleteMessage) Type() byte { return msgDelete }
