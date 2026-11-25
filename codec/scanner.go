// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// Scanner is a type that reads a column itself. Scan receives int64, float64,
// bool, []byte, string, time.Time, or nil for NULL. The []byte is the
// caller's to keep.
type Scanner interface {
	Scan(src any) error
}
