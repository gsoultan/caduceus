// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// BatchSender sends a batch: a Conn does, and so does a Tx.
type BatchSender interface {
	SendBatch(ctx context.Context, queries []Query) (*BatchResults, error)
}

var (
	_ BatchSender = (*Conn)(nil)
	_ BatchSender = (*Tx)(nil)
)
