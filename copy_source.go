// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// CopySource supplies the rows of a CopyFrom, one at a time, the way
// database/sql's Rows does: Next advances, Values returns the current row, and
// Err reports what stopped Next.
type CopySource interface {
	Next() bool
	Values() ([]any, error)
	Err() error
}

// copyRequest is where a CopyFrom goes and what it sends.
type copyRequest struct {
	Table   []string
	Columns []string
	Rows    CopySource
}
