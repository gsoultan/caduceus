// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "fmt"

// RowTo is a RowToFunc for a result of one column, scanned into a T.
//
//	ids, err := caduceus.CollectRows(rows, caduceus.RowTo[int64])
func RowTo[T any](row CollectableRow) (T, error) {
	var value T
	err := row.Scan(&value)
	return value, err
}

// RowToAddrOf is RowTo returning a pointer to the value, a fresh one for each
// row.
func RowToAddrOf[T any](row CollectableRow) (*T, error) {
	value := new(T)
	if err := row.Scan(value); err != nil {
		return nil, err
	}
	return value, nil
}

// RowToMap is a RowToFunc that maps each column's name to its value, decoded
// as Values decodes it. Every row has a map of its own. Of two columns with one
// name, the later is kept.
func RowToMap(row CollectableRow) (map[string]any, error) {
	values, err := row.Values()
	if err != nil {
		return nil, err
	}
	fields := row.Fields()
	if len(fields) != len(values) {
		return nil, fmt.Errorf("caduceus: %d columns described, %d values in the row", len(fields), len(values))
	}
	m := make(map[string]any, len(values))
	for i, value := range values {
		m[fields[i].Name] = value
	}
	return m, nil
}
