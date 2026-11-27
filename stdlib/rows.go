// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib

import (
	"database/sql/driver"
	"io"
	"reflect"

	"github.com/gsoultan/caduceus"
	"github.com/gsoultan/caduceus/wire"
)

// rows is a result, read off the connection a row at a time.
type rows struct {
	rows    *caduceus.Rows
	columns []string
}

var (
	_ driver.Rows                           = (*rows)(nil)
	_ driver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
	_ driver.RowsColumnTypeScanType         = (*rows)(nil)
	_ driver.RowsColumnTypeLength           = (*rows)(nil)
	_ driver.RowsColumnTypePrecisionScale   = (*rows)(nil)
)

// Columns are the result's column names.
func (r *rows) Columns() []string {
	if r.columns == nil {
		fields := r.rows.Fields()
		r.columns = make([]string, len(fields))
		for i := range fields {
			r.columns[i] = fields[i].Name
		}
	}
	return r.columns
}

// Close reads what is left of the result, so the connection is free again.
// It is idempotent.
func (r *rows) Close() error {
	r.rows.Close()
	return r.rows.Err()
}

// Next reads the next row into dest, as the types the package documents.
func (r *rows) Next(dest []driver.Value) error {
	if !r.rows.Next() {
		return r.end()
	}
	fields := r.rows.Fields()
	for i, raw := range r.rows.RawValues() {
		value, err := columnValue(&fields[i], raw)
		if err != nil {
			return err
		}
		dest[i] = value
	}
	return nil
}

// end is what Next returns once there are no more rows: the error that ended
// the result, or io.EOF.
func (r *rows) end() error {
	if err := r.rows.Err(); err != nil {
		return err
	}
	return io.EOF
}

// field describes column i.
func (r *rows) field(i int) *wire.Field { return &r.rows.Fields()[i] }

// ColumnTypeDatabaseTypeName is the column's type as PostgreSQL names it, in
// upper case, INT4 or _TEXT, or empty for a type the driver does not know.
func (r *rows) ColumnTypeDatabaseTypeName(i int) string {
	return typeNames[r.field(i).DataTypeOID]
}

// ColumnTypeScanType is the Go type Next gives the column.
func (r *rows) ColumnTypeScanType(i int) reflect.Type {
	return scanType(r.field(i).DataTypeOID)
}

// ColumnTypeLength is the declared length of a varchar(n) or char(n), and
// unlimited for text and bytea.
func (r *rows) ColumnTypeLength(i int) (int64, bool) {
	return length(r.field(i))
}

// ColumnTypePrecisionScale is the declared precision and scale of a
// numeric(p, s).
func (r *rows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	return precisionScale(r.field(i))
}
