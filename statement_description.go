// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"slices"

	"github.com/gsoultan/caduceus/wire"
)

// StatementDescription is what the server said of a statement prepared with
// Prepare: the types of its parameters and the columns of its result.
//
// It is a copy. Changing it changes nothing the connection holds, and a later
// Prepare that parses the statement afresh does not change it either.
type StatementDescription struct {
	// Name is the statement's name on the server, which is what EXECUTE and
	// pg_prepared_statements know it by. It is the name given to Prepare,
	// except for a name that could collide with another statement there: the
	// SQL text itself, a name longer than the server keeps (63 bytes), one
	// holding a NUL, or one beginning "caduceus_", which is how the statement
	// cache names its own. Those are given a name derived from a SHA-256
	// digest. The statement is run by the name given to Prepare either way.
	Name string
	// SQL is the statement's text.
	SQL string
	// ParamOIDs are the type OIDs the server resolved for $1, $2 and so on.
	ParamOIDs []uint32
	// Fields describe the result's columns, each with the format the driver
	// asks for it in; none for a statement that returns no rows.
	Fields []wire.Field
}

// describe returns a fresh description of a prepared statement.
func describe(stmt *statement) *StatementDescription {
	return &StatementDescription{
		Name:      stmt.name,
		SQL:       stmt.sql,
		ParamOIDs: slices.Clone(stmt.params),
		Fields:    slices.Clone(stmt.fields),
	}
}
