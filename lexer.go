// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "strings"

// literalEnd returns where a span of SQL starting at i that is not to be
// looked into ends: a string, a quoted identifier, a comment or a
// dollar-quoted string. It returns i when none starts there. An unterminated
// one runs to the end, for the server to refuse.
func literalEnd(sql string, i int) int {
	switch {
	case sql[i] == '\'':
		return quotedEnd(sql, i+1, '\'', escapeString(sql, i))
	case sql[i] == '"':
		return quotedEnd(sql, i+1, '"', false)
	case strings.HasPrefix(sql[i:], "--"):
		return lineEnd(sql, i)
	case strings.HasPrefix(sql[i:], "/*"):
		return blockEnd(sql, i)
	case sql[i] == '$':
		return dollarEnd(sql, i)
	}
	return i
}

// quotedEnd returns the end of a string or identifier quoted by quote whose
// text starts at from: a doubled quote is part of it, and in an escape string
// so is any character after a backslash.
func quotedEnd(sql string, from int, quote byte, backslashes bool) int {
	for k := from; k < len(sql); k++ {
		switch {
		case backslashes && sql[k] == '\\':
			k++
		case sql[k] == quote && k+1 < len(sql) && sql[k+1] == quote:
			k++
		case sql[k] == quote:
			return k + 1
		}
	}
	return len(sql)
}

// escapeString reports whether the string quoted at i is an escape string,
// E'...', where a backslash escapes what follows it.
func escapeString(sql string, i int) bool {
	return i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e') && (i == 1 || !identChar(sql[i-2]))
}

// lineEnd returns the end of the -- comment at i, its newline included.
func lineEnd(sql string, i int) int {
	if n := strings.IndexByte(sql[i:], '\n'); n >= 0 {
		return i + n + 1
	}
	return len(sql)
}

// blockEnd returns the end of the /* comment at i, which PostgreSQL lets
// nest.
func blockEnd(sql string, i int) int {
	depth := 0
	for k := i; k+1 < len(sql); k++ {
		switch {
		case sql[k] == '/' && sql[k+1] == '*':
			depth++
			k++
		case sql[k] == '*' && sql[k+1] == '/' && depth == 1:
			return k + 2
		case sql[k] == '*' && sql[k+1] == '/':
			depth--
			k++
		}
	}
	return len(sql)
}

// dollarEnd returns the end of the dollar-quoted string at i, $$...$$ or
// $tag$...$tag$, or i when none starts there: $1 is a parameter, and a $
// inside an identifier is part of it.
func dollarEnd(sql string, i int) int {
	if i > 0 && identChar(sql[i-1]) {
		return i
	}
	k := i + 1
	for k < len(sql) && nameChar(sql[k]) && !(k == i+1 && isDigit(sql[k])) {
		k++
	}
	if k >= len(sql) || sql[k] != '$' {
		return i
	}
	tag := sql[i : k+1]
	closing := strings.Index(sql[k+1:], tag)
	if closing < 0 {
		return len(sql)
	}
	return k + 1 + closing + len(tag)
}

// nameStart reports whether c can start a name: a letter, an underscore or
// a byte of a character beyond ASCII.
func nameStart(c byte) bool {
	return c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || c >= 0x80
}

// nameChar reports whether c can go on a name.
func nameChar(c byte) bool { return nameStart(c) || isDigit(c) }

// identChar reports whether c can be part of an unquoted identifier, which
// may hold a $.
func identChar(c byte) bool { return nameChar(c) || c == '$' }

func isDigit(c byte) bool { return '0' <= c && c <= '9' }
