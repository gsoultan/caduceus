// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// RelationColumn is a column of a relation, as a Relation message
// describes it. Flags is 1 for a column of the replica identity.
type RelationColumn struct {
	Flags        uint8
	Name         string
	DataType     uint32
	TypeModifier int32
}
