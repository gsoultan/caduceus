// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import (
	"errors"
	"fmt"
)

var errRecord = errors.New("caduceus/codec: malformed record")

// CompositeFields reads a composite value, a row of a composite type or a
// ROW(...), into its fields in order, each element a pointer to scan into,
// and writes one from its fields, each a value, nil for NULL. The record is
// read and written as text, so each field is read as a value of unknown type
// is: an int, a float, a string, a time, anything with an UnmarshalText.
type CompositeFields []any

func (c CompositeFields) scanColumn(oid uint32, format int16, src []byte) error {
	if src == nil {
		return ErrNull
	}
	text, err := asString(oid, format, src)
	if err != nil {
		return err
	}
	fields, err := parseRecord(text)
	if err != nil {
		return err
	}
	if len(fields) != len(c) {
		return fmt.Errorf("caduceus/codec: %d fields in the record, %d destinations", len(fields), len(c))
	}
	for i, field := range fields {
		if c[i] == nil {
			continue
		}
		if err := Scan(Unknown, 0, field, c[i]); err != nil {
			return fmt.Errorf("caduceus/codec: field %d: %w", i+1, err)
		}
	}
	return nil
}

func (c CompositeFields) encodeColumn(dst []byte, _ uint32) ([]byte, bool, error) {
	if c == nil {
		return dst, true, nil
	}
	dst = append(dst, '(')
	for i, field := range c {
		if i > 0 {
			dst = append(dst, ',')
		}
		text, null, err := EncodeText(nil, Unknown, field)
		if err != nil {
			return dst, false, fmt.Errorf("caduceus/codec: field %d: %w", i+1, err)
		}
		if !null {
			dst = appendQuoted(dst, text)
		}
	}
	return append(dst, ')'), false, nil
}

// parseRecord reads a record literal as record_out prints one: (1,abc,,"x y"),
// an empty unquoted field a NULL, a quoted one with "" or a backslash before
// a quote.
func parseRecord(text string) ([][]byte, error) {
	if len(text) < 2 || text[0] != '(' || text[len(text)-1] != ')' {
		return nil, errRecord
	}
	body := text[1 : len(text)-1]
	var fields [][]byte
	var field []byte
	quoted, present := false, false
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case quoted && c == '\\' && i+1 < len(body):
			i++
			field = append(field, body[i])
		case quoted && c == '"' && i+1 < len(body) && body[i+1] == '"':
			i++
			field = append(field, '"')
		case c == '"':
			quoted, present = !quoted, true
		case !quoted && c == ',':
			fields = append(fields, recordField(field, present))
			field, present = nil, false
		default:
			field, present = append(field, c), true
		}
	}
	if quoted {
		return nil, errRecord
	}
	return append(fields, recordField(field, present)), nil
}

// recordField is a field's bytes, nil for an empty unquoted field, which is
// NULL; an empty quoted one is the empty string.
func recordField(field []byte, present bool) []byte {
	if !present {
		return nil
	}
	if field == nil {
		return []byte{}
	}
	return field
}
