// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/gsoultan/caduceus/wire"
)

// structLayout is which field of a struct each column of a result is scanned
// into, derived once for a struct type, a mapping and a set of column names.
// It is never changed once made, so goroutines share it.
type structLayout struct {
	// columns holds the column names a by-name layout was derived for, to tell
	// a hash collision in the cache from a hit. A positional layout depends on
	// the number of columns alone, which its key holds, so it keeps none.
	columns []string
	// positional is a layout by position.
	positional bool
	// fields holds, for each column, the index of its field.
	fields [][]int
	err    error
}

// newStructLayout derives the layout of t for columns.
func newStructLayout(t reflect.Type, how mapping, columns []wire.Field) *structLayout {
	if t.Kind() != reflect.Struct {
		return &structLayout{err: fmt.Errorf("caduceus: %s is not a struct", t)}
	}
	fields := structFields(t, nil, nil)
	if how == byPos {
		layout := &structLayout{positional: true}
		layout.fields, layout.err = positional(t, fields, len(columns))
		return layout
	}
	layout := &structLayout{columns: make([]string, len(columns))}
	for i := range columns {
		layout.columns[i] = columns[i].Name
	}
	layout.fields, layout.err = named(t, fields, layout.columns, how == byNameLax)
	return layout
}

// size returns the bytes of column names the layout holds.
func (l *structLayout) size() int64 {
	var n int64
	for _, name := range l.columns {
		n += int64(len(name))
	}
	return n
}

// describes reports whether the layout was derived for these columns.
func (l *structLayout) describes(columns []wire.Field) bool {
	if l.positional {
		return true
	}
	if len(l.columns) != len(columns) {
		return false
	}
	for i := range columns {
		if l.columns[i] != columns[i].Name {
			return false
		}
	}
	return true
}

// structFields lists the fields of t that take a column, in order, with those
// of an embedded struct in its place, whether or not the embedded struct has
// a db tag, as pgx flattens it. An unexported field, and one tagged db:"-",
// takes none; so does an embedded struct tagged db:"-".
func structFields(t reflect.Type, prefix []int, out []structField) []structField {
	for i := range t.NumField() {
		out = appendField(t.Field(i), append(prefix[:len(prefix):len(prefix)], i), out)
	}
	return out
}

// appendField appends what sf contributes to the fields that take columns.
func appendField(sf reflect.StructField, index []int, out []structField) []structField {
	tag, _, _ := strings.Cut(sf.Tag.Get("db"), ",")
	switch {
	case tag == "-":
		return out
	case sf.Anonymous && sf.Type.Kind() == reflect.Struct:
		return structFields(sf.Type, index, out)
	case !sf.IsExported():
		return out
	case tag != "":
		return append(out, structField{goName: sf.Name, name: tag, tagged: true, index: index})
	}
	return append(out, structField{goName: sf.Name, name: sf.Name, index: index})
}

// positional matches the n-th column to the n-th field.
func positional(t reflect.Type, fields []structField, columns int) ([][]int, error) {
	if len(fields) != columns {
		return nil, fmt.Errorf("caduceus: %d columns in the result, %d fields in %s", columns, len(fields), t)
	}
	indexes := make([][]int, len(fields))
	for i := range fields {
		indexes[i] = fields[i].index
	}
	return indexes, nil
}

// named matches every column to the field of its name. Every column must have
// a field, no field two columns, and, unless lax, every field a column. A
// field that a shallower one of the same name shadows has the column too,
// as far as that last rule goes, though it is the shallower one that takes it.
func named(t reflect.Type, fields []structField, columns []string, lax bool) ([][]int, error) {
	indexes := make([][]int, len(columns))
	taken := make([]int, len(fields))
	covered := make([]bool, len(fields))
	for i, column := range columns {
		cover(fields, column, covered)
		f := fieldFor(fields, column)
		if f < 0 {
			return nil, fmt.Errorf("caduceus: column %q has no field in %s", column, t)
		}
		if taken[f] > 0 {
			return nil, fmt.Errorf("caduceus: columns %q and %q both map to field %s of %s", columns[taken[f]-1], column, fields[f].goName, t)
		}
		taken[f] = i + 1
		indexes[i] = fields[f].index
	}
	if lax {
		return indexes, nil
	}
	return indexes, everyFieldCovered(t, fields, covered)
}

// fieldFor returns the position of the field that takes column, or -1. When
// several fields match, it is the one Go promotes: the shallowest, so a
// struct's own field shadows an embedded one of the same name. Among fields
// equally deep, the last wins, as pgx lets a later match overwrite.
func fieldFor(fields []structField, column string) int {
	best := -1
	for i := range fields {
		if fields[i].matches(column) && shallower(fields, i, best) {
			best = i
		}
	}
	return best
}

// shallower reports whether field i is at most as deep as field best, or
// best is none yet.
func shallower(fields []structField, i, best int) bool {
	return best < 0 || len(fields[i].index) <= len(fields[best].index)
}

// cover marks every field that matches column, the one that takes it and the
// ones it shadows.
func cover(fields []structField, column string, covered []bool) {
	for i := range fields {
		covered[i] = covered[i] || fields[i].matches(column)
	}
}

// everyFieldCovered refuses a field that no column matches.
func everyFieldCovered(t reflect.Type, fields []structField, covered []bool) error {
	for i := range fields {
		if !covered[i] {
			return fmt.Errorf("caduceus: field %s of %s has no column in the result", fields[i].goName, t)
		}
	}
	return nil
}
