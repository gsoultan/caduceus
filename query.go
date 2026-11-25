// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// Query is one statement of a batch.
type Query struct {
	SQL       string
	Arguments []any
}
