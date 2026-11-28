// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import "unicode/utf8"

// saslprep prepares a password for SCRAM as PostgreSQL does, in
// pg_saslprep, so that what the client hashes is what the server hashed when
// the password was set: RFC 4013's SASLprep, with PostgreSQL's two
// exceptions. A password that is not valid UTF-8, or that SASLprep
// prohibits, is used as it is, and a password of ASCII alone is used as it is
// without being looked at.
//
// SASLprep maps non-ASCII spaces to a space and removes the characters
// commonly mapped to nothing, such as a soft hyphen; normalizes to NFKC,
// which turns a full-width A into A and composes an e and a combining acute
// into é; then prohibits control and private-use characters, code points
// unassigned in Unicode 3.2, and right-to-left text mixed with left-to-right.
func saslprep(password string) string {
	if ascii(password) || !utf8.ValidString(password) {
		return password
	}
	mapped := make([]rune, 0, len(password))
	for _, r := range password {
		switch {
		case inRanges(saslprepSpace, r):
			mapped = append(mapped, ' ')
		case inRanges(saslprepNothing, r):
		default:
			mapped = append(mapped, r)
		}
	}
	prepared := nfkc(mapped)
	if len(prepared) == 0 || prohibited(prepared) || !bidiAllowed(prepared) {
		return password
	}
	return string(prepared)
}

func ascii(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// prohibited reports whether any code point is one SASLprep prohibits.
func prohibited(runes []rune) bool {
	for _, r := range runes {
		if inRanges(saslprepProhibited, r) {
			return true
		}
	}
	return false
}

// bidiAllowed is RFC 3454's section 6: a string with right-to-left
// characters has no left-to-right ones, and begins and ends with a
// right-to-left one.
func bidiAllowed(runes []rune) bool {
	rightToLeft := false
	for _, r := range runes {
		rightToLeft = rightToLeft || inRanges(saslprepRandAL, r)
	}
	if !rightToLeft {
		return true
	}
	for _, r := range runes {
		if inRanges(saslprepL, r) {
			return false
		}
	}
	return inRanges(saslprepRandAL, runes[0]) && inRanges(saslprepRandAL, runes[len(runes)-1])
}
