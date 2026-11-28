// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// Tablespace is one tablespace a base backup sends, as the server lists
// them before it starts.
type Tablespace struct {
	// OID is the tablespace's, and zero for the main data directory.
	OID uint32
	// Location is its directory on the server, and empty for the main data
	// directory.
	Location string
	// Size is the server's estimate of its size in bytes, and -1 where
	// progress was not asked for.
	Size int64
}
