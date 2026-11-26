// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// InsertMessage is a row inserted.
type InsertMessage struct {
	Xid        uint32
	RelationID uint32
	Tuple      *TupleData
}

func (*InsertMessage) Type() byte { return msgInsert }
