// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

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
