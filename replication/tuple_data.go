// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// TupleData is a row of a pgoutput message, its columns in the order of
// its relation's.
type TupleData struct {
	Columns []TupleColumn
}
