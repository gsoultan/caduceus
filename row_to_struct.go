// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "reflect"

// RowToStructByPos is a RowToFunc that scans the n-th column into the n-th
// exported field of T, a struct. A field tagged db:"-" is skipped, and the
// fields of an embedded struct, tagged or not, count in its place. The result must have as
// many columns as T has such fields.
func RowToStructByPos[T any](row CollectableRow) (T, error) {
	var value T
	err := scanStruct(row, &value, byPos)
	return value, err
}

// RowToAddrOfStructByPos is RowToStructByPos returning a pointer to the
// struct, a fresh one for each row.
func RowToAddrOfStructByPos[T any](row CollectableRow) (*T, error) {
	return addrOf[T](row, byPos)
}

// RowToStructByName is a RowToFunc that scans each column into the exported
// field of T, a struct, of its name. A field's name is its db tag, matched
// exactly as written, or else its Go name, matched without regard to case or
// underscores, so UserID takes user_id. A field tagged db:"-" is skipped, and
// the fields of an embedded struct, tagged or not, are matched as T's own,
// except that T's own field shadows an embedded one of the same name, as in
// Go. Every column must have a field and every field a column, a shadowed
// field counting as having its shadow's, and no field two columns.
//
// The match is derived once for each struct type and set of columns, and
// kept for the next row and the next query.
func RowToStructByName[T any](row CollectableRow) (T, error) {
	var value T
	err := scanStruct(row, &value, byName)
	return value, err
}

// RowToAddrOfStructByName is RowToStructByName returning a pointer to the
// struct, a fresh one for each row.
func RowToAddrOfStructByName[T any](row CollectableRow) (*T, error) {
	return addrOf[T](row, byName)
}

// RowToStructByNameLax is RowToStructByName that leaves a field without a
// column at its zero value. Every column must still have a field.
func RowToStructByNameLax[T any](row CollectableRow) (T, error) {
	var value T
	err := scanStruct(row, &value, byNameLax)
	return value, err
}

// RowToAddrOfStructByNameLax is RowToStructByNameLax returning a pointer to the
// struct, a fresh one for each row.
func RowToAddrOfStructByNameLax[T any](row CollectableRow) (*T, error) {
	return addrOf[T](row, byNameLax)
}

// addrOf scans the row into a fresh struct and returns its address.
func addrOf[T any](row CollectableRow, how mapping) (*T, error) {
	value := new(T)
	if err := scanStruct(row, value, how); err != nil {
		return nil, err
	}
	return value, nil
}

// scanStruct scans the row into the struct ptr points to, by the layout of
// its type for the row's columns.
func scanStruct[T any](row CollectableRow, ptr *T, how mapping) error {
	v := reflect.ValueOf(ptr).Elem()
	layout := layouts.layout(v.Type(), how, row.Fields())
	if layout.err != nil {
		return layout.err
	}
	dest := make([]any, len(layout.fields))
	for i, index := range layout.fields {
		dest[i] = v.FieldByIndex(index).Addr().Interface()
	}
	return row.Scan(dest...)
}
