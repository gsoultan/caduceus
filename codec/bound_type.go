// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// BoundType is how a Range is bounded at one end.
type BoundType byte

const (
	Inclusive BoundType = 'i'
	Exclusive BoundType = 'e'
	Unbounded BoundType = 'U'
	// Empty is both bounds of the empty range.
	Empty BoundType = 'E'
)
