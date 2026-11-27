// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package gss

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/gsoultan/caduceus"
	"github.com/gsoultan/caduceus/wire"
)

// Against a server whose pg_hba.conf says gss for the role, with a ticket
// kinit left in KRB5CCNAME. Skipped unless DATABASE_URL_GSS is set.
func liveConfig(t *testing.T) *wire.Config {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_GSS")
	if dsn == "" {
		t.Skip("DATABASE_URL_GSS not set")
	}
	config, err := wire.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

// The login is Kerberos end to end: the server says the session was
// authenticated by GSSAPI, and its reply proved it held the session key.
func TestLiveKerberosLogin(t *testing.T) {
	config := liveConfig(t)
	config.NewGSS = New
	conn, err := caduceus.ConnectConfig(t.Context(), config, caduceus.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var user string
	var authenticated bool
	if err := conn.QueryRow(t.Context(), "SELECT current_user, gss_authenticated FROM pg_stat_gssapi WHERE pid = pg_backend_pid()").Scan(&user, &authenticated); err != nil {
		t.Fatal(err)
	}
	if user != config.User || !authenticated {
		t.Errorf("logged in as %q, GSSAPI authenticated %v", user, authenticated)
	}
}

// Without a provider the login fails, saying how to give it one.
func TestLiveKerberosWithoutAProvider(t *testing.T) {
	config := liveConfig(t)
	if _, err := caduceus.ConnectConfig(t.Context(), config, caduceus.Options{}); !errors.Is(err, wire.ErrNoGSS) {
		t.Fatalf("Connect = %v, want ErrNoGSS", err)
	}
}

// A principal the KDC does not know is refused before anything reaches the
// server.
func TestLiveKerberosUnknownService(t *testing.T) {
	config := liveConfig(t)
	config.NewGSS = New
	config.KerberosServiceName = "nosuchservice"
	if _, err := caduceus.ConnectConfig(t.Context(), config, caduceus.Options{}); err == nil {
		t.Fatal("Connect succeeded for a principal the KDC does not know")
	}
}

// What is not a Kerberos reply, or one that does not prove the server holds
// the session key, fails the login.
func TestContinueRefusesWhatIsNotAProof(t *testing.T) {
	var k Kerberos
	if _, _, err := k.Continue([]byte("not a token")); err == nil {
		t.Error("Continue accepted bytes that are not a token")
	}
}

// A cache named by KRB5CCNAME is read with or without its FILE: prefix, and
// one of another kind is refused with a reason.
func TestCachePath(t *testing.T) {
	for name, want := range map[string]string{"FILE:/tmp/cc": "/tmp/cc", "/tmp/cc2": "/tmp/cc2"} {
		t.Setenv("KRB5CCNAME", name)
		if got, err := cachePath(); err != nil || got != want {
			t.Errorf("KRB5CCNAME=%s: %q, %v", name, got, err)
		}
	}
	t.Setenv("KRB5CCNAME", "KEYRING:persistent:1000")
	if _, err := cachePath(); err == nil {
		t.Error("a KEYRING cache was taken for a file")
	}
}
