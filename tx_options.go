// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "strings"

// IsoLevel is a transaction isolation level.
type IsoLevel string

// The isolation levels PostgreSQL implements. The empty level is the server's
// default, default_transaction_isolation.
const (
	ReadCommitted  IsoLevel = "READ COMMITTED"
	RepeatableRead IsoLevel = "REPEATABLE READ"
	Serializable   IsoLevel = "SERIALIZABLE"
	// ReadUncommitted behaves as ReadCommitted in PostgreSQL.
	ReadUncommitted IsoLevel = "READ UNCOMMITTED"
)

// TxOptions are a transaction's characteristics.
type TxOptions struct {
	IsoLevel IsoLevel
	ReadOnly bool
	// Deferrable has effect only on a serializable, read-only transaction,
	// which then waits for a snapshot that cannot fail on serialization.
	Deferrable bool
}

// beginSQL is the BEGIN that sets the options. The isolation level is checked
// against the known ones, since it is written into the statement rather than
// bound.
func (o TxOptions) beginSQL() (string, error) {
	if o == (TxOptions{}) {
		// The usual transaction, and one string built the fewer for it.
		return "BEGIN", nil
	}
	var b strings.Builder
	b.WriteString("BEGIN")
	switch o.IsoLevel {
	case "":
	case ReadCommitted, RepeatableRead, Serializable, ReadUncommitted:
		b.WriteString(" ISOLATION LEVEL ")
		b.WriteString(string(o.IsoLevel))
	default:
		return "", ErrIsoLevel
	}
	if o.ReadOnly {
		b.WriteString(" READ ONLY")
	}
	if o.Deferrable {
		b.WriteString(" DEFERRABLE")
	}
	return b.String(), nil
}
