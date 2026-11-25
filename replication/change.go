// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// Change is one row change.
//
// Before is the old row as far as the table's REPLICA IDENTITY covers it: nil
// for an insert, and for an update under the default identity unless the key
// changed. After is nil for a delete. A TOASTed value that did not change is
// absent from the map, which is not the same as a NULL, which is present as nil.
//
// Values are strings, exactly as pgoutput sends them in text format. The maps
// are allocated fresh for every change and belong to the caller.
type Change struct {
	Op          Op
	Relation    *Relation
	Transaction Transaction
	Before      map[string]any
	After       map[string]any
}
