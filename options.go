// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "github.com/gsoultan/caduceus/codec"

// DefaultStatementCacheCapacity is how many prepared statements a connection
// keeps on the server when Options does not say.
const DefaultStatementCacheCapacity = 64

// DefaultDescriptionCacheCapacity is how many statements a connection that
// keeps none on the server remembers the description of when Options does not
// say.
const DefaultDescriptionCacheCapacity = 64

// Options tunes a Conn.
type Options struct {
	// StatementCacheCapacity is how many prepared statements the connection
	// keeps on the server, least recently used first out. Zero means
	// DefaultStatementCacheCapacity.
	//
	// Negative keeps none. A statement is then parsed into the unnamed
	// statement every time it runs, in the same Sync as its Bind, so nothing on
	// the server outlives a Sync. That is what a pooler in transaction mode
	// needs, which is free to give every Sync a different server connection,
	// and what a server that runs DISCARD ALL between callers needs.
	StatementCacheCapacity int

	// DescriptionCacheCapacity matters only when no statements are kept on the
	// server. It is how many statements the connection remembers the parameter
	// and column types of, least recently used first out, so that a statement
	// it has run before takes one round trip and not a second to ask about it
	// first. Nothing is kept on the server for it. Zero means
	// DefaultDescriptionCacheCapacity; negative remembers nothing, and every
	// statement is asked about before it runs.
	//
	// What is remembered is held to the server's word. A statement is parsed
	// with the parameter types it was described with, as a prepared statement
	// keeps the types it was prepared with; its columns are checked against
	// what the server says of them on every execution, and a result whose
	// columns changed is read as the server describes it, with
	// ErrResultChanged for a column that cannot be read in the format its old
	// type was asked for in; and an error before the server has bound the
	// statement forgets it.
	DescriptionCacheCapacity int

	// Tracer, if set, is told of every statement the connection runs; see
	// QueryTracer. A tracer that is also a BatchTracer, a CopyFromTracer, a
	// PrepareTracer or a ConnectTracer is told of those too. A connection
	// without one pays a nil check for it and nothing more.
	Tracer QueryTracer

	// ResultFormat chooses the format, 0 for text and 1 for binary, each
	// result column of type oid is asked for in. Nil asks for the formats
	// codec.ResultFormat gives, which every Scan destination reads.
	//
	// It is for a caller that reads RawValues and decodes them itself, such
	// as an ORM whose generated scanners decode binary: numeric, jsonb, inet
	// and cidr are asked for in text by default. A column asked for in a
	// format the codec does not read is still given by RawValues, and Scan
	// and Values return a *codec.TypeError for it.
	ResultFormat func(oid uint32) int16
}

// resultFormat returns the function that chooses each column's format.
func (o Options) resultFormat() func(uint32) int16 {
	if o.ResultFormat != nil {
		return o.ResultFormat
	}
	return codec.ResultFormat
}

// statements is how many statements to keep on the server: none when the
// cache is off.
func (o Options) statements() int {
	switch {
	case o.StatementCacheCapacity < 0:
		return 0
	case o.StatementCacheCapacity == 0:
		return DefaultStatementCacheCapacity
	}
	return o.StatementCacheCapacity
}

// descriptions is how many descriptions to remember of statements that are
// not kept on the server.
func (o Options) descriptions() int {
	switch {
	case o.DescriptionCacheCapacity < 0:
		return 0
	case o.DescriptionCacheCapacity == 0:
		return DefaultDescriptionCacheCapacity
	}
	return o.DescriptionCacheCapacity
}
