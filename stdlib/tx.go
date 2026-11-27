// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib

import (
	"context"
	"database/sql/driver"

	"github.com/gsoultan/caduceus"
)

// tx is a transaction. Its statements run on the connection, which sends the
// deferred BEGIN in front of the first.
type tx struct {
	tx *caduceus.Tx
}

var _ driver.Tx = tx{}

// Commit commits the transaction. database/sql gives it no context, and
// rolls the transaction back itself when the one given to BeginTx is done;
// a commit, once sent, is waited for.
func (t tx) Commit() error { return t.tx.Commit(context.Background()) }

// Rollback rolls the transaction back, within teardownTimeout: it is
// teardown, which a dead server must not hold up.
func (t tx) Rollback() error {
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	return t.tx.Rollback(ctx)
}
