// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// CopyFromFunc is a CopySource whose rows next returns one at a time, until it
// returns a nil row, which ends them, or an error, which ends the copy.
func CopyFromFunc(next func() (row []any, err error)) CopySource {
	return &copyFunc{next: next}
}

type copyFunc struct {
	next func() ([]any, error)
	row  []any
	err  error
}

func (c *copyFunc) Next() bool {
	if c.err != nil {
		return false
	}
	c.row, c.err = c.next()
	return c.err == nil && c.row != nil
}

func (c *copyFunc) Values() ([]any, error) { return c.row, nil }

func (c *copyFunc) Err() error { return c.err }
