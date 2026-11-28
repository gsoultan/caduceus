// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The normalization agrees with golang.org/x/text's NFKC on every code point
// it changes and on thousands of strings mixing decompositions and
// combining marks: tools/saslprep wrote what x/text makes of each.
func TestNFKCVectors(t *testing.T) {
	file, err := os.Open("testdata/nfkc_vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lines, failures := 0, 0
	for scanner.Scan() {
		input, want := vector(t, scanner.Text())
		lines++
		if got := string(nfkc([]rune(input))); got != want && failures < 20 {
			failures++
			t.Errorf("nfkc(%+q) = %+q, want %+q", input, got, want)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines < 5000 {
		t.Fatalf("%d vectors, want the file's thousands", lines)
	}
}

func vector(t *testing.T, line string) (string, string) {
	t.Helper()
	prefix, err := strconv.QuotedPrefix(line)
	if err != nil {
		t.Fatalf("vector %q: %v", line, err)
	}
	input, _ := strconv.Unquote(prefix)
	want, err := strconv.Unquote(strings.TrimSpace(line[len(prefix):]))
	if err != nil {
		t.Fatalf("vector %q: %v", line, err)
	}
	return input, want
}

// SASLprep as pg_saslprep does it. A password it rewrites used to be hashed
// as typed, which a server that had prepared it refused.
func TestSASLprep(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"ASCII is untouched, controls and all", "pass\x01word ", "pass\x01word "},
		{"invalid UTF-8 is used as it is", "ｐass\xffword", "ｐass\xffword"},
		{"full-width letters are normalized", "ｐａｓｓ", "pass"},
		{"a decomposed accent is composed", "café", "café"},
		{"a ligature is decomposed", "ﬁx", "fix"},
		{"a no-break space is a space", "a b", "a b"},
		{"a soft hyphen is removed", "pass­word", "password"},
		{"a zero-width space is a space, which wins over nothing", "a​b", "a b"},
		{"RFC 4013's example: I and the roman numeral IV", "I­X", "IX"},
		{"a mark blocked by one of its class does not compose", "a̅́", "a̅́"},
		{"a mark past one of a lower class composes", "ạ́", "ạ́"},
		{"Hangul composes", "각", "각"},
		// Prohibited: the password is used as it is, mapping and all.
		{"a control character prohibits", "ｐass\u0080word", "ｐass\u0080word"},
		{"private use prohibits", "ｐass", "ｐass"},
		{"unassigned in Unicode 3.2 prohibits", "ｐass\U0001F600", "ｐass\U0001F600"},
		{"right-to-left mixed with left-to-right prohibits", "אａב", "אａב"},
		{"right-to-left that does not begin with it prohibits", "１א", "１א"},
		{"right-to-left that does not end with it prohibits", "א１", "א１"},
		{"right-to-left alone is allowed", "א ב", "א ב"},
		{"neutral digits inside right-to-left are allowed", "א１ב", "א1ב"},
		{"mapped to nothing at all is used as it is", "­", "­"},
	}
	for _, tt := range tests {
		if got := saslprep(tt.in); got != tt.want {
			t.Errorf("%s: saslprep(%+q) = %+q, want %+q", tt.name, tt.in, got, tt.want)
		}
	}
}

// A password SASLprep rewrites logs in as typed, and as what it normalizes
// to, as with psql. Before, the client hashed it as typed while the server
// had hashed it prepared, and the login was refused.
func TestLiveSASLprepPassword(t *testing.T) {
	conn := liveConn(t)
	if os.Getenv("DATABASE_URL_SCRAM") == "" {
		t.Skip("DATABASE_URL_SCRAM not set")
	}
	scram, err := ParseConfig(os.Getenv("DATABASE_URL_SCRAM"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	typed := "ｐａｓｓ\u00adword"
	setPassword(t, conn, scram.User, typed)
	t.Cleanup(func() { setPassword(t, conn, scram.User, scram.Password) })
	for _, password := range []string{typed, "password"} {
		login := *scram
		login.Password = password
		other, err := Connect(ctx, &login)
		if err != nil {
			t.Errorf("login with %+q: %v", password, err)
			continue
		}
		_ = other.Close(ctx)
	}
	login := *scram
	login.Password = "passwore"
	if other, err := Connect(ctx, &login); err == nil {
		_ = other.Close(ctx)
		t.Fatal("a wrong password logged in: the server is not checking it")
	}
}

func setPassword(t *testing.T, conn *Conn, role, password string) {
	t.Helper()
	sql := "SET password_encryption = 'scram-sha-256'; ALTER ROLE " + quoteIdent(role) + " PASSWORD " + quoteLit(password)
	if _, err := conn.Exec(context.WithoutCancel(t.Context()), sql); err != nil {
		t.Fatalf("set the password of %s: %v", role, err)
	}
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func quoteLit(s string) string   { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
