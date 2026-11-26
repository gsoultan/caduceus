// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// TupleColumn is one column of a row in a pgoutput message. Kind is
// TupleNull, TupleText, TupleBinary or TupleUnchanged, an unchanged TOASTed
// value, which is absent and not the same as NULL; Data is the value, for
// TupleText and TupleBinary, and aliases the record.
type TupleColumn struct {
	Kind byte
	Data []byte
}
