// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// Valuer is a type that supplies its own parameter value. Value returns one of
// the types Encode handles natively, such as int64, float64, bool, []byte,
// string or time.Time, or nil for NULL.
type Valuer interface {
	Value() (any, error)
}
