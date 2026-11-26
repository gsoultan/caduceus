// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// CopyFromSlice is a CopySource of n rows, the i-th of which next returns.
func CopyFromSlice(n int, next func(i int) ([]any, error)) CopySource {
	return &copySlice{n: n, next: next, i: -1}
}

type copySlice struct {
	n    int
	next func(int) ([]any, error)
	i    int
	err  error
}

func (c *copySlice) Next() bool {
	c.i++
	return c.i < c.n
}

func (c *copySlice) Values() ([]any, error) {
	values, err := c.next(c.i)
	c.err = err
	return values, err
}

func (c *copySlice) Err() error { return c.err }
