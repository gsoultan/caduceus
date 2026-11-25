// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// Message types of the replication stream, the first byte of each CopyData.
const (
	// XLogData carries one WAL record.
	XLogData = 'w'
	// PrimaryKeepalive is the server asking for, or offering, a heartbeat.
	PrimaryKeepalive = 'k'
	// StandbyStatus is the client confirming how far it has got.
	StandbyStatus = 'r'
)

// Message types of the pgoutput plugin's protocol version 1.
const (
	msgBegin    = 'B'
	msgRelation = 'R'
	msgInsert   = 'I'
	msgUpdate   = 'U'
	msgDelete   = 'D'

	tupleNew = 'N' // the row as written
	tupleKey = 'K' // the replica identity's columns of the old row
	tupleOld = 'O' // the whole old row, under REPLICA IDENTITY FULL

	tupleNull      = 'n' // SQL NULL
	tupleText      = 't' // value present, in text format
	tupleUnchanged = 'u' // TOASTed value that did not change, not transmitted
	tupleBinary    = 'b' // value present, in binary format; never requested here
)

// postgresEpoch is the zero of the server's timestamps.
var postgresEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
