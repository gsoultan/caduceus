// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "errors"

var (
	// ErrConnBusy is a call on a connection whose previous result has not been
	// read to its end or closed.
	ErrConnBusy = errors.New("caduceus: connection is busy with an unread result")

	// ErrNoRows is QueryRow's Scan, CollectOneRow or CollectExactlyOneRow
	// finding no row.
	ErrNoRows = errors.New("caduceus: no rows in result set")

	// ErrTooManyRows is CollectExactlyOneRow finding a second row.
	ErrTooManyRows = errors.New("caduceus: too many rows in result set")

	// ErrNotOnARow is Scan called before Next, or after Next returned false.
	ErrNotOnARow = errors.New("caduceus: Scan called without a current row; call Next first")

	// ErrTxClosed is a transaction used after Commit or Rollback, or after a
	// later transaction began on its connection.
	ErrTxClosed = errors.New("caduceus: transaction already closed")

	// ErrTxCommitRollback is Commit on a transaction that had already failed,
	// which the server rolls back instead.
	ErrTxCommitRollback = errors.New("caduceus: commit unexpectedly resulted in rollback")

	// ErrTxNotBegun is a transaction whose BEGIN the server refused, as it
	// does a serializable one on a hot standby. BEGIN travels with the first
	// statement, so that statement reports the server's error and does not
	// run. From then until the transaction is settled, this is what the Tx
	// returns, and what the connection returns for a statement run on it
	// directly: the caller believes a transaction is open, and nothing may run
	// outside it.
	ErrTxNotBegun = errors.New("caduceus: the transaction was not begun: the server refused its BEGIN")

	// ErrInTransaction is Begin on a connection already in a transaction.
	ErrInTransaction = errors.New("caduceus: connection is already in a transaction")

	// ErrResultChanged is a column that cannot be read because the
	// statement's result changed type since the connection described it, on
	// a connection that keeps no statements on the server. The result was
	// asked for in the format that suited the type the column had, the
	// server ran the statement and sent the column in that format, and the
	// codec cannot read the type it has now in that format. The statement
	// ran: what it wrote is written. What was remembered of it is dropped,
	// so the next execution is described afresh and reads it. A statement
	// kept on the server is refused before it runs instead, with SQLSTATE
	// 0A000.
	ErrResultChanged = errors.New("caduceus: the statement ran, but its result changed type since it was described and cannot be read in the format asked for")

	// ErrIsoLevel is a TxOptions.IsoLevel that is not one of the declared levels.
	ErrIsoLevel = errors.New("caduceus: unknown isolation level")

	// ErrEmptyBatch is SendBatch or SendPipeline with no queries.
	ErrEmptyBatch = errors.New("caduceus: batch is empty")

	// ErrNoCopySource is CopyFrom with a nil source of rows.
	ErrNoCopySource = errors.New("caduceus: CopyFrom needs a source of rows")
)
