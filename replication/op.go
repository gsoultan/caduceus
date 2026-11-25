// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// Op is the kind of row change.
type Op byte

// The row changes pgoutput reports.
const (
	Insert Op = 'I'
	Update Op = 'U'
	Delete Op = 'D'
)

// String names the operation.
func (o Op) String() string {
	switch o {
	case Insert:
		return "INSERT"
	case Update:
		return "UPDATE"
	case Delete:
		return "DELETE"
	}
	return "UNKNOWN"
}
