// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "context"

// TxBeginner begins a transaction: a Conn does, and so does a Tx, whose
// Begin is a savepoint.
type TxBeginner interface {
	Begin(ctx context.Context) (*Tx, error)
}

var (
	_ TxBeginner = (*Conn)(nil)
	_ TxBeginner = (*Tx)(nil)
)
