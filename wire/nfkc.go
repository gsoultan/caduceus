// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"slices"
	"sort"
)

// inRanges reports whether r is in sorted, disjoint ranges.
func inRanges(ranges []runeRange, r rune) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i].hi >= r })
	return i < len(ranges) && ranges[i].lo <= r
}

// The Hangul syllables decompose and compose by arithmetic, as the Unicode
// standard defines in section 3.12.
const (
	hangulS     = 0xAC00
	hangulL     = 0x1100
	hangulV     = 0x1161
	hangulT     = 0x11A7
	hangulLN    = 19
	hangulVN    = 21
	hangulTN    = 28
	hangulNN    = hangulVN * hangulTN
	hangulSN    = hangulLN * hangulNN
	hangulSLast = hangulS + hangulSN - 1
)

// nfkc normalizes runes to Normalization Form KC: the full compatibility
// decomposition, put in canonical order, then canonically composed. It is
// written for SASLprep, whose input is a password: short, and normalized
// once a login.
func nfkc(runes []rune) []rune {
	decomposed := make([]rune, 0, len(runes))
	for _, r := range runes {
		decomposed = appendDecomposition(decomposed, r)
	}
	canonicalOrder(decomposed)
	return compose(decomposed)
}

// appendDecomposition appends r's full compatibility decomposition.
func appendDecomposition(dst []rune, r rune) []rune {
	if r >= hangulS && r <= hangulSLast {
		index := r - hangulS
		dst = append(dst, hangulL+index/hangulNN, hangulV+(index%hangulNN)/hangulTN)
		if t := index % hangulTN; t != 0 {
			dst = append(dst, hangulT+t)
		}
		return dst
	}
	i, found := slices.BinarySearch(nfkdRunes[:], r)
	if !found {
		return append(dst, r)
	}
	for _, d := range nfkdData[nfkdOffsets[i]:nfkdOffsets[i+1]] {
		dst = append(dst, d)
	}
	return dst
}

// combiningClass is r's canonical combining class.
func combiningClass(r rune) uint8 {
	i := sort.Search(len(combiningClasses), func(i int) bool { return combiningClasses[i].hi >= r })
	if i < len(combiningClasses) && combiningClasses[i].lo <= r {
		return combiningClasses[i].class
	}
	return 0
}

// canonicalOrder sorts each run of combining marks by class, keeping the
// order of marks of one class.
func canonicalOrder(runes []rune) {
	for start := 0; start < len(runes); {
		if combiningClass(runes[start]) == 0 {
			start++
			continue
		}
		end := start
		for end < len(runes) && combiningClass(runes[end]) != 0 {
			end++
		}
		slices.SortStableFunc(runes[start:end], func(a, b rune) int {
			return int(combiningClass(a)) - int(combiningClass(b))
		})
		start = end
	}
}

// compose is canonical composition: each character joined to the last
// starter before it, unless something between them blocks it, which a
// starter or a mark of the same or a higher class does. Two adjacent
// starters may join, as a Hangul LV syllable and a trailing consonant do.
func compose(runes []rune) []rune {
	if len(runes) == 0 {
		return runes
	}
	out := runes[:1]
	// starter is out's last starter, -1 before the first. adjacent says
	// nothing stands between it and the next character; otherwise lastClass is
	// the class of the last character after it.
	starter, adjacent, lastClass := 0, true, 0
	if combiningClass(runes[0]) != 0 {
		starter = -1
	}
	for _, r := range runes[1:] {
		class := int(combiningClass(r))
		if starter >= 0 && (adjacent || lastClass < class) {
			if composed, ok := composePair(out[starter], r); ok {
				out[starter] = composed
				continue
			}
		}
		if class == 0 {
			starter, adjacent = len(out), true
		} else {
			adjacent, lastClass = false, class
		}
		out = append(out, r)
	}
	return out
}

// composePair is what a followed by b composes into, if anything.
func composePair(a, b rune) (rune, bool) {
	if composed, ok := composeHangul(a, b); ok {
		return composed, true
	}
	i, found := slices.BinarySearch(compositionKeys[:], uint64(a)<<21|uint64(b))
	if !found {
		return 0, false
	}
	return compositionRunes[i], true
}

// composeHangul joins a leading consonant and a vowel into an LV syllable,
// and an LV syllable and a trailing consonant into an LVT one.
func composeHangul(a, b rune) (rune, bool) {
	switch {
	case a >= hangulL && a < hangulL+hangulLN && b >= hangulV && b < hangulV+hangulVN:
		return hangulS + ((a-hangulL)*hangulVN+(b-hangulV))*hangulTN, true
	case a >= hangulS && a <= hangulSLast && (a-hangulS)%hangulTN == 0 && b > hangulT && b < hangulT+hangulTN:
		return a + (b - hangulT), true
	}
	return 0, false
}
