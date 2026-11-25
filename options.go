// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// DefaultStatementCacheCapacity is how many prepared statements a connection
// keeps on the server when Options does not say.
const DefaultStatementCacheCapacity = 64

// Options tunes a Conn.
type Options struct {
	// StatementCacheCapacity is how many prepared statements the connection
	// keeps on the server, least recently used first out. Zero means
	// DefaultStatementCacheCapacity. Negative disables the cache: every query is
	// parsed into the unnamed statement, which is what a pooler in transaction
	// mode, or a server that runs DISCARD ALL between callers, needs.
	StatementCacheCapacity int
}

func (o Options) statements() int {
	switch {
	case o.StatementCacheCapacity < 0:
		return 0
	case o.StatementCacheCapacity == 0:
		return DefaultStatementCacheCapacity
	}
	return o.StatementCacheCapacity
}
