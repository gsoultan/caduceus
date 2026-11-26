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

	msgCommit   = 'C'
	msgOrigin   = 'O'
	msgType     = 'Y'
	msgTruncate = 'T'
	msgMessage  = 'M'

	// Version 2: transactions streamed before they commit.
	msgStreamStart  = 'S'
	msgStreamStop   = 'E'
	msgStreamCommit = 'c'
	msgStreamAbort  = 'A'

	// Version 3: two-phase commit.
	msgBeginPrepare     = 'b'
	msgPrepare          = 'P'
	msgCommitPrepared   = 'K'
	msgRollbackPrepared = 'r'
	msgStreamPrepare    = 'p'

	tupleNew = 'N' // the row as written
	tupleKey = 'K' // the replica identity's columns of the old row
	tupleOld = 'O' // the whole old row, under REPLICA IDENTITY FULL

	tupleNull      = TupleNull
	tupleText      = TupleText
	tupleUnchanged = TupleUnchanged
	tupleBinary    = TupleBinary
)

// The kinds of a TupleColumn, and of an old row.
const (
	TupleNull      = 'n' // SQL NULL
	TupleText      = 't' // value present, in text format
	TupleUnchanged = 'u' // TOASTed value that did not change, not transmitted
	TupleBinary    = 'b' // value present, in binary format, with binary on

	TupleKey = 'K' // the old row's replica identity columns
	TupleOld = 'O' // the whole old row, under REPLICA IDENTITY FULL
)

// postgresEpoch is the zero of the server's timestamps.
var postgresEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
