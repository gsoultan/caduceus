// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib

import (
	"database/sql/driver"

	"github.com/gsoultan/caduceus"
)

// result is a statement's outcome, read from its command tag.
type result struct {
	tag caduceus.CommandTag
}

var _ driver.Result = result{}

// LastInsertId returns ErrNoLastInsertID: PostgreSQL has none.
func (result) LastInsertId() (int64, error) { return 0, ErrNoLastInsertID }

// RowsAffected is how many rows the statement touched, from its command tag.
func (r result) RowsAffected() (int64, error) { return r.tag.RowsAffected(), nil }
