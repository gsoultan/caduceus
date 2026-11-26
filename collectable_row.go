// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "github.com/gsoultan/caduceus/wire"

// CollectableRow is the current row of a result, as a RowToFunc sees it.
// *Rows is one. Its values alias the connection's read buffer until the next
// row, so a RowToFunc decodes or copies what it keeps.
type CollectableRow interface {
	// Fields describes the result's columns.
	Fields() []wire.Field
	// Scan decodes the row into dest, one destination per column.
	Scan(dest ...any) error
	// Values decodes every column into a freshly allocated slice.
	Values() ([]any, error)
	// RawValues returns the row's undecoded values.
	RawValues() [][]byte
}

var _ CollectableRow = (*Rows)(nil)
