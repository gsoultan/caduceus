// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import "time"

// Transaction is what a Begin message says about the transaction whose
// changes follow it.
type Transaction struct {
	// FinalLSN is where the transaction's commit record is in the WAL.
	FinalLSN LSN
	// Committed is when the transaction committed on the server.
	Committed time.Time
	// XID is the transaction's id.
	XID uint32
}

// parseBegin reads a Begin message after its type byte.
func parseBegin(body []byte) (Transaction, error) {
	r := reader{data: body}
	tx := Transaction{FinalLSN: LSN(r.uint64()), Committed: r.timestamp(), XID: r.uint32()}
	return tx, r.err
}
