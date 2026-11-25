// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// CopyFromRows is a CopySource over rows already in memory.
func CopyFromRows(rows [][]any) CopySource {
	return &rowSlice{rows: rows, next: -1}
}

// rowSlice is the CopySource CopyFromRows returns.
type rowSlice struct {
	rows [][]any
	next int
}

func (r *rowSlice) Next() bool {
	r.next++
	return r.next < len(r.rows)
}

func (r *rowSlice) Values() ([]any, error) { return r.rows[r.next], nil }

func (r *rowSlice) Err() error { return nil }
