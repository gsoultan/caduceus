// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// Several hosts are read as libpq reads them, from key=value strings and from
// URLs. A second host used to be refused outright.
func TestParseSeveralHosts(t *testing.T) {
	tests := []struct {
		name, dsn string
		want      []string
	}{
		{"keywords, a port each", "host=a,b port=1,2 user=u sslmode=disable", []string{"a:1", "b:2"}},
		{"keywords, one port for all", "host=a,b,c port=7 user=u sslmode=disable", []string{"a:7", "b:7", "c:7"}},
		{"url", "postgres://u@a:1,b:2/db?sslmode=disable", []string{"a:1", "b:2"}},
		{"url, a port missing", "postgres://u@a,b:2/db?sslmode=disable", []string{"a:5432", "b:2"}},
		{"url, a socket directory", "postgres://u@%2Fvar%2Frun%2Fpostgresql:5433/db", []string{"/var/run/postgresql:5433"}},
		{"url, ipv6", "postgres://u@[::1]:3,b/db?sslmode=disable", []string{"::1:3", "b:5432"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := ParseConfig(test.dsn)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, server := range config.servers() {
				got = append(got, server.Host+":"+strconv.Itoa(int(server.Port)))
			}
			if strings.Join(got, " ") != strings.Join(test.want, " ") {
				t.Fatalf("servers = %v, want %v", got, test.want)
			}
		})
	}
	if _, err := ParseConfig("host=a,b,c port=1,2 user=u"); !errors.Is(err, ErrInvalidConnString) {
		t.Errorf("three hosts and two ports = %v, want ErrInvalidConnString", err)
	}
}

// Each host's certificate is checked against its own name, and a copy of the
// configuration shares nothing of a fallback's with the template.
func TestFallbackTLSAndCopy(t *testing.T) {
	config, err := ParseConfig("host=x,b user=u sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	if config.TLSConfig.ServerName != "x" || config.Fallbacks[0].TLSConfig.ServerName != "b" {
		t.Fatalf("server names %q, %q", config.TLSConfig.ServerName, config.Fallbacks[0].TLSConfig.ServerName)
	}
	clone := config.Copy()
	clone.Fallbacks[0].Host = "changed"
	clone.Fallbacks[0].TLSConfig.ServerName = "changed"
	if config.Fallbacks[0].Host != "b" || config.Fallbacks[0].TLSConfig.ServerName != "b" {
		t.Fatal("changing a copy's fallback changed the template's")
	}
}

// target_session_attrs sets the validator libpq's value names, and refuses
// what it does not know. Every value but any used to be refused.
func TestParseTargetSessionAttrs(t *testing.T) {
	for _, attrs := range []string{"read-write", "read-only", "primary", "standby", "prefer-standby"} {
		config, err := ParseConfig("host=a user=u target_session_attrs=" + attrs)
		if err != nil || config.ValidateConnect == nil {
			t.Errorf("%s: %v, validator set %v", attrs, err, config != nil && config.ValidateConnect != nil)
		}
	}
	if config, err := ParseConfig("host=a user=u target_session_attrs=any"); err != nil || config.ValidateConnect != nil {
		t.Errorf("any: %v", err)
	}
	if _, err := ParseConfig("host=a user=u target_session_attrs=leader"); !errors.Is(err, ErrInvalidConnString) {
		t.Errorf("leader = %v, want ErrInvalidConnString", err)
	}
}

// liveServers is a configuration for the two servers of the test setup, the
// primary and its standby, in the order given.
func liveServers(t *testing.T, first, second string) *Config {
	t.Helper()
	primary, standby := os.Getenv("DATABASE_URL"), os.Getenv("STANDBY_URL")
	if primary == "" || standby == "" {
		t.Skip("DATABASE_URL and STANDBY_URL not set")
	}
	urls := map[string]string{"primary": primary, "standby": standby}
	a, err := ParseConfig(urls[first])
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseConfig(urls[second])
	if err != nil {
		t.Fatal(err)
	}
	a.Fallbacks = []*FallbackConfig{{Host: b.Host, Port: b.Port, TLSConfig: b.TLSConfig, TLSFallback: b.TLSFallback}}
	return a
}

// A server that cannot be reached is passed over for the next, and the
// connection's configuration is the server it reached, which is where a
// cancel request must go.
func TestLiveFallbackToTheNextServer(t *testing.T) {
	config := liveConfig(t)
	reached := config.Port
	config.Fallbacks = []*FallbackConfig{{Host: config.Host, Port: reached}}
	config.Host, config.Port = "127.0.0.1", 1
	conn, err := Connect(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if conn.Config().Port != reached || conn.Config().Fallbacks != nil {
		t.Fatalf("connected as %s:%d with %d fallbacks", conn.Config().Host, conn.Config().Port, len(conn.Config().Fallbacks))
	}
	if _, err := conn.Exec(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
}

// Every server failing is a ConnectError for each, joined, the first first;
// a wrong database ends the attempt at the first, since no other server would
// have it either.
func TestLiveEveryServerFailing(t *testing.T) {
	config := liveConfig(t)
	config.Fallbacks = []*FallbackConfig{{Host: "127.0.0.1", Port: 2}}
	config.Host, config.Port = "127.0.0.1", 1
	_, err := Connect(t.Context(), config)
	var first *ConnectError
	if !errors.As(err, &first) || first.Port != 1 || !strings.Contains(err.Error(), "port=2") {
		t.Fatalf("Connect = %v, want a ConnectError for each server", err)
	}

	config = liveConfig(t)
	config.Database = "caduceus_no_such_database"
	config.Fallbacks = []*FallbackConfig{{Host: "127.0.0.1", Port: 1}}
	_, err = Connect(t.Context(), config)
	if !errors.As(err, &first) || strings.Contains(err.Error(), "port=1") {
		t.Fatalf("Connect = %v, want it to stop at the missing database", err)
	}
}

// target_session_attrs picks the server of the kind asked for, whichever
// order they are listed in, and prefer-standby falls back to a primary.
func TestLiveTargetSessionAttrs(t *testing.T) {
	tests := []struct {
		first, second string
		validate      func(context.Context, *Conn) error
		want          string
	}{
		{"standby", "primary", ValidatePrimary, "off"},
		{"standby", "primary", ValidateReadWrite, "off"},
		{"primary", "standby", ValidateStandby, "on"},
		{"primary", "standby", ValidateReadOnly, "on"},
		{"primary", "standby", ValidatePreferStandby, "on"},
		{"primary", "primary", ValidatePreferStandby, "off"},
	}
	for _, test := range tests {
		config := liveServers(t, test.first, test.second)
		config.ValidateConnect = test.validate
		conn, err := Connect(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		if got := conn.ParameterStatus("in_hot_standby"); got != test.want {
			t.Errorf("%s then %s: connected to in_hot_standby=%s, want %s", test.first, test.second, got, test.want)
		}
		_ = conn.Close(t.Context())
	}

	config := liveServers(t, "standby", "standby")
	config.ValidateConnect = ValidatePrimary
	if _, err := Connect(t.Context(), config); !errors.Is(err, ErrSessionAttrs) {
		t.Fatalf("no primary = %v, want ErrSessionAttrs", err)
	}
}

// A server older than PostgreSQL 14 reports neither in_hot_standby nor
// default_transaction_read_only, and is asked.
func TestLiveSessionAttrsByQuery(t *testing.T) {
	for _, server := range []string{"primary", "standby"} {
		config := liveServers(t, server, server)
		conn, err := Connect(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		delete(conn.params, "in_hot_standby")
		delete(conn.params, "default_transaction_read_only")
		standby := server == "standby"
		if err := ValidateStandby(t.Context(), conn); (err == nil) != standby {
			t.Errorf("%s: ValidateStandby = %v", server, err)
		}
		if err := ValidateReadOnly(t.Context(), conn); (err == nil) != standby {
			t.Errorf("%s: ValidateReadOnly = %v", server, err)
		}
		_ = conn.Close(t.Context())
	}

	// A server that reports one of the two and not the other is asked too:
	// what it reports of one says nothing of the other.
	conn, err := Connect(t.Context(), liveConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(t.Context(), "SET default_transaction_read_only = on"); err != nil {
		t.Fatal(err)
	}
	delete(conn.params, "default_transaction_read_only")
	if err := ValidateReadOnly(t.Context(), conn); err != nil {
		t.Errorf("a primary whose sessions are read-only: ValidateReadOnly = %v", err)
	}
}

// DialFunc dials, LookupFunc's addresses are each tried, and an AfterConnect
// that fails closes the connection and fails Connect.
func TestLiveConnectHooks(t *testing.T) {
	config := liveConfig(t)
	var dialled atomic.Int32
	config.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialled.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	host := config.Host
	config.Host = "caduceus.invalid"
	config.LookupFunc = func(ctx context.Context, name string) ([]string, error) {
		if name != "caduceus.invalid" {
			t.Errorf("looked up %q", name)
		}
		return []string{"127.0.0.2", host}, nil
	}
	if config.Port == 0 {
		t.Fatal("no port")
	}
	var set atomic.Bool
	config.AfterConnect = func(ctx context.Context, conn *Conn) error {
		set.Store(true)
		_, err := conn.Exec(ctx, "SET application_name = 'caduceus-hooks'")
		return err
	}
	conn, err := Connect(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if conn.ParameterStatus("application_name") != "caduceus-hooks" || !set.Load() || dialled.Load() < 1 {
		t.Fatalf("application_name %q, AfterConnect run %v, dialled %d", conn.ParameterStatus("application_name"), set.Load(), dialled.Load())
	}
	_ = conn.Close(t.Context())

	refused := errors.New("refused")
	config.AfterConnect = func(context.Context, *Conn) error { return refused }
	if _, err := Connect(t.Context(), config); !errors.Is(err, refused) {
		t.Fatalf("Connect with AfterConnect failing = %v", err)
	}
}
