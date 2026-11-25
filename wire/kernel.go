// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

// kernelAtLeast reports whether a kernel release such as "6.18.44-fc" is at
// least major.minor. A release it cannot read is not.
func kernelAtLeast(release string, major, minor int) bool {
	gotMajor, rest, ok := leadingNumber(release)
	if !ok || len(rest) == 0 || rest[0] != '.' {
		return false
	}
	gotMinor, _, ok := leadingNumber(rest[1:])
	if !ok {
		return false
	}
	return gotMajor > major || (gotMajor == major && gotMinor >= minor)
}

// leadingNumber reads the decimal digits text starts with.
func leadingNumber(text string) (int, string, bool) {
	n, i := 0, 0
	for i < len(text) && text[i] >= '0' && text[i] <= '9' && i < 6 {
		n = n*10 + int(text[i]-'0')
		i++
	}
	return n, text[i:], i > 0
}
