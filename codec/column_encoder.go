// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// columnEncoder is a type of this package that writes a parameter given its
// type, which a Valuer is not told: a Range writes its bounds as its element
// type. It reports null for NULL, and always writes text.
type columnEncoder interface {
	encodeColumn(dst []byte, oid uint32) ([]byte, bool, error)
}
