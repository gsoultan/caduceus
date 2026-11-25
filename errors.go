// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "errors"

var (
	// ErrConnBusy is a call on a connection whose previous result has not been
	// read to its end or closed.
	ErrConnBusy = errors.New("caduceus: connection is busy with an unread result")

	// ErrNoRows is QueryRow's Scan finding no row.
	ErrNoRows = errors.New("caduceus: no rows in result set")

	// ErrNotOnARow is Scan called before Next, or after Next returned false.
	ErrNotOnARow = errors.New("caduceus: Scan called without a current row; call Next first")

	// ErrTxClosed is a transaction used after Commit or Rollback.
	ErrTxClosed = errors.New("caduceus: transaction already closed")

	// ErrTxCommitRollback is Commit on a transaction that had already failed,
	// which the server rolls back instead.
	ErrTxCommitRollback = errors.New("caduceus: commit unexpectedly resulted in rollback")

	// ErrInTransaction is Begin on a connection already in a transaction.
	ErrInTransaction = errors.New("caduceus: connection is already in a transaction")

	// ErrIsoLevel is a TxOptions.IsoLevel that is not one of the declared levels.
	ErrIsoLevel = errors.New("caduceus: unknown isolation level")

	// ErrEmptyBatch is SendBatch with no queries.
	ErrEmptyBatch = errors.New("caduceus: batch is empty")
)
