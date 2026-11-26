// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// RelationMessage describes a relation before the first change to it, and
// again after it changes. Xid is the transaction's, inside a stream, and
// zero outside one.
type RelationMessage struct {
	Xid             uint32
	RelationID      uint32
	Namespace       string
	RelationName    string
	ReplicaIdentity uint8
	Columns         []RelationColumn
}

func (*RelationMessage) Type() byte { return msgRelation }
