// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "errors"

// errNilRows and errNilRowTo are a collecting function handed nil rows, as Query returns with
// an error, or a nil RowToFunc.
var (
	errNilRows  = errors.New("caduceus: rows are nil; check the error Query returned")
	errNilRowTo = errors.New("caduceus: RowToFunc is nil")
)

// collectable refuses what a collecting function cannot read, so it returns an
// error rather than dereferencing nil.
func collectable[T any](rows *Rows, fn RowToFunc[T]) error {
	switch {
	case rows == nil:
		return errNilRows
	case fn == nil:
		rows.Close()
		return errNilRowTo
	}
	return nil
}

// CollectRows calls fn for every row and returns the results. It closes rows,
// whatever happens, and returns the error that ended them, or the first one
// fn returns.
//
//	rows, err := conn.Query(ctx, "SELECT id, name FROM users")
//	if err != nil {
//		return err
//	}
//	users, err := caduceus.CollectRows(rows, caduceus.RowToStructByName[User])
func CollectRows[T any](rows *Rows, fn RowToFunc[T]) ([]T, error) {
	return AppendRows([]T{}, rows, fn)
}

// AppendRows is CollectRows appending to slice. On an error it returns nil.
func AppendRows[T any, S ~[]T](slice S, rows *Rows, fn RowToFunc[T]) (S, error) {
	if err := collectable(rows, fn); err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		value, err := fn(rows)
		if err != nil {
			return nil, err
		}
		slice = append(slice, value)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return slice, nil
}

// CollectOneRow calls fn for the first row and returns the result, or
// ErrNoRows when there is none. Any rows after the first are read off the
// connection undecoded. It closes rows, whatever happens.
func CollectOneRow[T any](rows *Rows, fn RowToFunc[T]) (T, error) {
	var zero T
	if err := collectable(rows, fn); err != nil {
		return zero, err
	}
	defer rows.Close()
	if !rows.Next() {
		return zero, noRow(rows)
	}
	value, err := fn(rows)
	if err != nil {
		return zero, err
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return zero, err
	}
	return value, nil
}

// CollectExactlyOneRow is CollectOneRow that returns ErrTooManyRows when the
// result has more than one row.
func CollectExactlyOneRow[T any](rows *Rows, fn RowToFunc[T]) (T, error) {
	var zero T
	if err := collectable(rows, fn); err != nil {
		return zero, err
	}
	defer rows.Close()
	if !rows.Next() {
		return zero, noRow(rows)
	}
	value, err := fn(rows)
	if err != nil {
		return zero, err
	}
	if rows.Next() {
		return zero, ErrTooManyRows
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return zero, err
	}
	return value, nil
}

// noRow is why a result that was to have a row has none: the error that ended
// it, or ErrNoRows.
func noRow(rows *Rows) error {
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return ErrNoRows
}

// ForEachRow scans every row into scans and calls fn after each, and returns
// the command tag. It closes rows, whatever happens, and stops at the first
// error, its own, Scan's or fn's.
//
//	var id int64
//	var name string
//	tag, err := caduceus.ForEachRow(rows, []any{&id, &name}, func() error {
//		fmt.Println(id, name)
//		return nil
//	})
func ForEachRow(rows *Rows, scans []any, fn func() error) (CommandTag, error) {
	if rows == nil {
		return "", errNilRows
	}
	defer rows.Close()
	for rows.Next() {
		if err := forRow(rows, scans, fn); err != nil {
			return "", err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	return rows.Tag(), nil
}

// forRow scans one row into scans and hands it to fn, which may be nil.
func forRow(rows *Rows, scans []any, fn func() error) error {
	if err := rows.Scan(scans...); err != nil {
		return err
	}
	if fn == nil {
		return nil
	}
	return fn()
}
