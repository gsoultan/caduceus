// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// mapping is how the columns of a row are matched to a struct's fields.
type mapping uint8

const (
	// byPos matches the n-th column to the n-th field.
	byPos mapping = iota
	// byName matches columns to fields by name, and wants a column for every
	// field.
	byName
	// byNameLax is byName that leaves a field without a column alone.
	byNameLax
)
