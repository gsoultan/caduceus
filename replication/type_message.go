// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// TypeMessage describes a type a relation's column has that is not
// built in, before the Relation message that uses it.
type TypeMessage struct {
	Xid       uint32
	DataType  uint32
	Namespace string
	Name      string
}

func (*TypeMessage) Type() byte { return msgType }
