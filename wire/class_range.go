// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

// classRange is a range of code points of one canonical combining class.
type classRange struct {
	lo, hi rune
	class  uint8
}
