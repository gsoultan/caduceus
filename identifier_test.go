// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "testing"

// An identifier or a literal that reaches SQL as text must not be able to
// end itself early: a quote inside is doubled, so what a caller passes stays
// one name or one string. gpool kept its own copies of these, tested there;
// it now calls these, so the cases live here.
func TestQuoting(t *testing.T) {
	tests := []struct {
		name  string
		quote func(string) string
		input string
		want  string
	}{
		{"identifier", QuoteIdentifier, "users", `"users"`},
		{"identifier keeps case", QuoteIdentifier, "Users", `"Users"`},
		{"identifier doubles a quote", QuoteIdentifier, `we"ird`, `"we""ird"`},
		{"identifier neutralises injection", QuoteIdentifier, `x"; DROP TABLE y; --`, `"x""; DROP TABLE y; --"`},
		{"identifier with a dot is one name", QuoteIdentifier, "public.users", `"public.users"`},
		{"literal", QuoteLiteral, "pub", `'pub'`},
		{"literal doubles a quote", QuoteLiteral, "it's", `'it''s'`},
		{"literal neutralises injection", QuoteLiteral, `a', injected '`, `'a'', injected '''`},
		{"literal with a backslash is an escape string", QuoteLiteral, `back\slash`, `E'back\\slash'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.quote(tt.input); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}
