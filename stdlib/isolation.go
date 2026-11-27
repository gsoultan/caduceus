// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib

import (
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/gsoultan/caduceus"
)

// isolationLevels maps database/sql's levels to PostgreSQL's. It is never
// written after initialisation.
var isolationLevels = map[sql.IsolationLevel]caduceus.IsoLevel{
	sql.LevelDefault:         "",
	sql.LevelReadUncommitted: caduceus.ReadUncommitted,
	sql.LevelReadCommitted:   caduceus.ReadCommitted,
	sql.LevelRepeatableRead:  caduceus.RepeatableRead,
	sql.LevelSerializable:    caduceus.Serializable,
}

// txOptions translates database/sql's transaction options.
func txOptions(options driver.TxOptions) (caduceus.TxOptions, error) {
	level := sql.IsolationLevel(options.Isolation)
	iso, ok := isolationLevels[level]
	if !ok {
		return caduceus.TxOptions{}, fmt.Errorf("%w: %s", ErrIsolationLevel, level)
	}
	return caduceus.TxOptions{IsoLevel: iso, ReadOnly: options.ReadOnly}, nil
}
