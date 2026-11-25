// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package codec

import "strconv"

// The sign word of a binary numeric: its sign, or which special value it is.
const (
	numericPositive = 0x0000
	numericNegative = 0x4000
	numericNaN      = 0xC000
	numericInfinity = 0xD000
	numericMinusInf = 0xF000
)

// numericBase is what each digit of a binary numeric counts in.
const numericBase = 10000

// binaryNumeric renders a numeric's binary form as the text the server prints
// for it, and reports whether src was one.
//
// Numeric is asked for as text, whose digits are what a caller wants. Its
// binary form arrives only when a statement asked for binary for a column that
// was another type when it was described, an integer altered to numeric being
// the commonest, and reading it is what lets that result be read at all.
//
// The form is a count of base 10000 digits, the weight of the first, a sign
// word and the scale to print: the server prints every digit of the integer
// part, and then the scale's worth of the fraction, its missing digits zero.
func binaryNumeric(src []byte) (string, bool) {
	if len(src) < 8 {
		return "", false
	}
	count, weight, scale := int(be16(src)), int(be16(src[2:])), int(be16(src[6:]))
	digits := src[8:]
	if count < 0 || scale < 0 || len(digits) != 2*count {
		return "", false
	}
	switch uint16(be16(src[4:])) {
	case numericNaN:
		return "NaN", true
	case numericInfinity:
		return "Infinity", true
	case numericMinusInf:
		return "-Infinity", true
	case numericNegative:
		return printNumeric([]byte{'-'}, digits, weight, scale)
	case numericPositive:
		return printNumeric(nil, digits, weight, scale)
	}
	return "", false
}

// printNumeric appends the digits of a numeric to text, as numeric_out
// prints them.
func printNumeric(text, digits []byte, weight, scale int) (string, bool) {
	digit := func(i int) (int, bool) {
		if i < 0 || 2*i >= len(digits) {
			return 0, true
		}
		d := int(be16(digits[2*i:]))
		return d, d >= 0 && d < numericBase
	}
	if weight < 0 {
		text = append(text, '0')
	}
	for i := 0; i <= weight; i++ {
		d, ok := digit(i)
		if !ok {
			return "", false
		}
		if i == 0 {
			text = strconv.AppendInt(text, int64(d), 10)
			continue
		}
		text = appendFourDigits(text, d)
	}
	if scale == 0 {
		return string(text), true
	}
	text = append(text, '.')
	point := len(text)
	for i := weight + 1; len(text)-point < scale; i++ {
		d, ok := digit(i)
		if !ok {
			return "", false
		}
		text = appendFourDigits(text, d)
	}
	return string(text[:point+scale]), true
}

func appendFourDigits(text []byte, d int) []byte {
	return append(text, byte('0'+d/1000), byte('0'+d/100%10), byte('0'+d/10%10), byte('0'+d%10))
}
