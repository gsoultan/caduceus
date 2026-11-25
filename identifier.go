// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "strings"

// QuoteIdentifier renders s as one quoted SQL identifier, for the places SQL
// takes a name rather than a parameter, such as LISTEN or a table in DDL.
// "public.users" becomes one identifier naming a table called public.users;
// quote the schema and the table separately to qualify a name.
func QuoteIdentifier(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// QuoteLiteral renders s as a SQL string literal, for the places SQL takes a
// literal rather than a parameter. Bind values as parameters everywhere else.
//
// A string with a backslash is written as an escape string, E'...', with the
// backslash doubled, so it means the same whatever standard_conforming_strings
// is set to.
func QuoteLiteral(s string) string {
	quoted := `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
	if strings.Contains(s, `\`) {
		return "E" + strings.ReplaceAll(quoted, `\`, `\\`)
	}
	return quoted
}

func quoteIdentifier(s string) string { return QuoteIdentifier(s) }
