// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

// Package stdlib is a database/sql driver over caduceus, for code written
// against database/sql and for the libraries that take a *sql.DB: migration
// tools, query builders, ORMs. Importing it registers the driver as
// "caduceus":
//
//	import _ "github.com/gsoultan/caduceus/stdlib"
//
//	db, err := sql.Open("caduceus", "postgres://app@db.internal/app")
//
// OpenDB does the same from a wire.Config, with caduceus.Options, for a
// configuration a connection string cannot carry, such as a GSSAPI provider
// or a tracer.
//
// Placeholders are PostgreSQL's own, $1, $2 and so on, and arguments are bound
// as parameters as caduceus binds them, never interpolated: anything
// caduceus.Conn.Exec accepts, which includes a driver.Valuer, a slice for an
// array and a struct for json. sql.Named is refused; a caduceus.NamedArgs
// given as the only argument names them instead, written @name in the query.
//
// Columns arrive as the types database/sql documents for a driver: int64 for
// the integer types and oid, float64 for real and double precision, bool,
// []byte for bytea, time.Time for date, timestamp and timestamptz, and
// otherwise a string, the column's text as PostgreSQL writes it, which is
// what numeric, uuid, interval, json and arrays read best into. An infinite
// date or timestamp is the string infinity or -infinity.
//
// The *caduceus.Conn underneath is reached through sql.Conn's Raw, as a
// *Conn, for what database/sql has no word for: COPY, batches, LISTEN.
//
// The pool is database/sql's, which hands a connection to one caller at a
// time and checks it with IsValid and ResetSession before each. A statement
// prepared with DB.Prepare is prepared on the server, once on each
// connection it runs on; behind a pooler in transaction mode, prepare it on a
// Tx, or not at all.
package stdlib
