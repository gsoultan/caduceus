// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"encoding/binary"
	"testing"
)

// rowDescription encodes a RowDescription body.
func rowDescription(fields ...Field) []byte {
	body := binary.BigEndian.AppendUint16(nil, uint16(len(fields)))
	for _, field := range fields {
		body = append(append(body, field.Name...), 0)
		body = binary.BigEndian.AppendUint32(body, field.TableOID)
		body = binary.BigEndian.AppendUint16(body, field.TableAttributeNumber)
		body = binary.BigEndian.AppendUint32(body, field.DataTypeOID)
		body = binary.BigEndian.AppendUint16(body, uint16(field.DataTypeSize))
		body = binary.BigEndian.AppendUint32(body, uint32(field.TypeModifier))
		body = binary.BigEndian.AppendUint16(body, uint16(field.Format))
	}
	return body
}

// A description that is remembered is compared with the server's every time
// its statement runs, and read with only if nothing about it has changed. A
// difference missed here is a result read as types it does not have.
func TestSameRowDescription(t *testing.T) {
	id := Field{Name: "id", TableOID: 16384, TableAttributeNumber: 1, DataTypeOID: 23, DataTypeSize: 4, TypeModifier: -1}
	note := Field{Name: "note", TableOID: 16384, TableAttributeNumber: 2, DataTypeOID: 25, DataTypeSize: -1, TypeModifier: -1}
	changed := func(change func(*Field)) []byte {
		other := id
		change(&other)
		return rowDescription(other, note)
	}
	whole := rowDescription(id, note)
	cases := []struct {
		name string
		body []byte
		want bool
	}{
		{"the same", whole, true},
		{"the same in another format", changed(func(f *Field) { f.Format = BinaryFormat }), true},
		{"another name", changed(func(f *Field) { f.Name = "no" }), false},
		{"a longer name", changed(func(f *Field) { f.Name = "ident" }), false},
		{"a shorter name", changed(func(f *Field) { f.Name = "i" }), false},
		{"no name", changed(func(f *Field) { f.Name = "" }), false},
		{"another table", changed(func(f *Field) { f.TableOID++ }), false},
		{"another column of the table", changed(func(f *Field) { f.TableAttributeNumber++ }), false},
		{"another type", changed(func(f *Field) { f.DataTypeOID = 20 }), false},
		{"another size", changed(func(f *Field) { f.DataTypeSize = 8 }), false},
		{"another modifier", changed(func(f *Field) { f.TypeModifier = 14 }), false},
		{"a column fewer", rowDescription(id), false},
		{"a column more", rowDescription(id, note, note), false},
		{"the columns the other way round", rowDescription(note, id), false},
		{"cut short", whole[:len(whole)-1], false},
		{"cut short in a name", whole[:3], false},
		{"no count", nil, false},
	}
	fields := []Field{id, note}
	for _, c := range cases {
		if got := SameRowDescription(fields, c.body); got != c.want {
			t.Errorf("%s: SameRowDescription = %v, want %v", c.name, got, c.want)
		}
	}
	if !SameRowDescription(nil, rowDescription()) {
		t.Error("no columns were not the same as no columns")
	}
	if SameRowDescription(nil, whole) {
		t.Error("two columns were the same as none")
	}
	// It runs for every execution of a statement that is parsed every time.
	if allocated := testing.AllocsPerRun(100, func() { SameRowDescription(fields, whole) }); allocated != 0 {
		t.Errorf("SameRowDescription allocated %v times a run, want none", allocated)
	}
}
