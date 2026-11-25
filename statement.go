// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"github.com/gsoultan/caduceus/codec"
	"github.com/gsoultan/caduceus/wire"
)

// statement is a statement and what the server said about it: the types of its
// parameters, which decide how each argument is encoded, and the columns of
// its result, which decide the format each is asked for in.
type statement struct {
	// name is the statement's on the server, or empty for one that is not kept
	// there and is parsed into the unnamed statement every time it runs.
	name   string
	sql    string
	params []uint32
	fields []wire.Field
	// formats is the result format code for each column, already in the form a
	// Bind wants it: empty when every column is text, one code when they all
	// agree, one per column otherwise.
	formats []int16

	// prev and next link the statement into its cache's recency list.
	prev, next *statement
}

// newStatement builds a statement from a ParameterDescription and an optional
// RowDescription. The fields are given the format they will arrive in.
func newStatement(name, sql string, params []uint32, fields []wire.Field) *statement {
	s := &statement{name: name, sql: sql, params: params, fields: fields}
	allText, allBinary := true, true
	for i := range s.fields {
		format := codec.ResultFormat(s.fields[i].DataTypeOID)
		s.fields[i].Format = format
		allText = allText && format == wire.TextFormat
		allBinary = allBinary && format == wire.BinaryFormat
	}
	switch {
	case allText:
	case allBinary:
		s.formats = []int16{wire.BinaryFormat}
	default:
		s.formats = make([]int16, len(s.fields))
		for i, field := range s.fields {
			s.formats[i] = field.Format
		}
	}
	return s
}

// restated is the statement as the server now describes its result, for
// reading a result that was asked for in s's formats: every column keeps the
// format it was requested in, whatever type it turns out to have.
func (s *statement) restated(fields []wire.Field) *statement {
	for i := range fields {
		fields[i].Format = s.requested(i)
	}
	return &statement{sql: s.sql, params: s.params, fields: fields, formats: s.formats}
}

// requested returns the format column i of the result was asked for in.
func (s *statement) requested(i int) int16 {
	switch {
	case len(s.formats) == 1:
		return s.formats[0]
	case i < len(s.formats):
		return s.formats[i]
	}
	// No formats asks for text. More columns than formats is a Bind the server
	// refuses, so there is no result to read.
	return wire.TextFormat
}

// describes reports whether a RowDescription or a NoData is what stmt's
// columns were remembered as.
func describes(stmt *statement, kind byte, body []byte) bool {
	if kind == wire.MsgNoData {
		return len(stmt.fields) == 0
	}
	return wire.SameRowDescription(stmt.fields, body)
}

// restate puts fresh in place of old wherever a batch or a pipeline was going
// to read a result with old.
func restate(stmts []*statement, old, fresh *statement) {
	if old == fresh {
		return
	}
	for i := range stmts {
		if stmts[i] == old {
			stmts[i] = fresh
		}
	}
}
