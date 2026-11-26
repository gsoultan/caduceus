// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "strings"

// structField is a field a struct's columns can be scanned into: one of its
// own, or of a struct embedded in it, which index reaches.
type structField struct {
	// goName is the field's name in Go, for errors.
	goName string
	// name is the column it takes: its db tag, or its Go name.
	name string
	// tagged is a name from a db tag, which is matched exactly as written, as
	// pgx matches it. An untagged name is matched without regard to case or
	// underscores.
	tagged bool
	index  []int
}

// matches reports whether the field takes the named column.
func (f *structField) matches(column string) bool {
	if f.tagged {
		return f.name == column
	}
	return strings.EqualFold(withoutUnderscores(f.name), withoutUnderscores(column))
}

func withoutUnderscores(name string) string {
	return strings.ReplaceAll(name, "_", "")
}
