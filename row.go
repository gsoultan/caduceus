// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// Row is the result of QueryRow.
type Row struct {
	rows Rows
	err  error
	done bool
}

// Scan reads the first row into dest and closes the result. It returns
// ErrNoRows when the query produced nothing. A second call returns ErrNoRows
// too, having nothing left to read.
func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.done {
		return ErrNoRows
	}
	r.done = true
	defer r.rows.Close()
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return ErrNoRows
	}
	if err := r.rows.Scan(dest...); err != nil {
		return err
	}
	r.rows.Close()
	return r.rows.Err()
}

// Close finishes the row without reading it, so the connection is free. Scan
// does this itself; Close is for a row that is not going to be scanned.
func (r *Row) Close() {
	if r.err == nil && !r.done {
		r.done = true
		r.rows.Close()
	}
}
