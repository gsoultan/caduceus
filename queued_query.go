// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// QueuedQuery is a statement of a Batch and what to do with its result.
type QueuedQuery struct {
	SQL       string
	Arguments []any
	handler   func(*BatchResults) error
}

// Query hands the statement's rows to fn, which need not read them all. The
// rows are closed after it.
func (q *QueuedQuery) Query(fn func(rows *Rows) error) {
	q.handler = func(results *BatchResults) error {
		rows, err := results.Query()
		if err != nil {
			return err
		}
		defer rows.Close()
		if err := fn(rows); err != nil {
			return err
		}
		rows.Close()
		return rows.Err()
	}
}

// QueryRow hands the statement's first row to fn.
func (q *QueuedQuery) QueryRow(fn func(row *Row) error) {
	q.handler = func(results *BatchResults) error {
		return fn(results.QueryRow())
	}
}

// Exec hands the statement's command tag to fn.
func (q *QueuedQuery) Exec(fn func(tag CommandTag) error) {
	q.handler = func(results *BatchResults) error {
		tag, err := results.Exec()
		if err != nil {
			return err
		}
		return fn(tag)
	}
}

// handle reads the statement's result, through its callback if it has one.
func (q *QueuedQuery) handle(results *BatchResults) error {
	if q.handler == nil {
		_, err := results.Exec()
		return err
	}
	return q.handler(results)
}
