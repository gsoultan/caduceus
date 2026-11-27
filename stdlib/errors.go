// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib

import "errors"

var (
	// ErrNamedArg is returned for an argument given with sql.Named. PostgreSQL
	// numbers its parameters; for names, give a caduceus.NamedArgs as the only
	// argument and write them @name.
	ErrNamedArg = errors.New("caduceus/stdlib: sql.Named is not supported; use $1, $2, or a caduceus.NamedArgs as the only argument")
	// ErrOutArg is returned for a sql.Out argument: PostgreSQL returns what a
	// procedure gives back as a row, which Query reads.
	ErrOutArg = errors.New("caduceus/stdlib: sql.Out is not supported; read a procedure's results with Query")
	// ErrIsolationLevel is returned by BeginTx for a level PostgreSQL does not
	// have: sql.LevelSnapshot, sql.LevelWriteCommitted, sql.LevelLinearizable.
	ErrIsolationLevel = errors.New("caduceus/stdlib: isolation level not supported by PostgreSQL")
	// ErrNoLastInsertID is returned by Result.LastInsertId. PostgreSQL has no
	// such thing; INSERT ... RETURNING gives the new row's columns.
	ErrNoLastInsertID = errors.New("caduceus/stdlib: LastInsertId is not supported; use INSERT ... RETURNING")
)
