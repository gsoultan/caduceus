// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

// columnScanner is a type of this package that reads a column given its type,
// which a Scanner is not told: a Range reads its bounds as its element type.
type columnScanner interface {
	scanColumn(oid uint32, format int16, src []byte) error
}
