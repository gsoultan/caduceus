// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// Every libpq setting about the connection rather than the session stays on
// the client. Each used to reach the server in the startup packet, which
// refused the connection: unrecognized configuration parameter.
func TestTransportKeysAreNotSent(t *testing.T) {
	settings := "host=a user=u keepalives=1 keepalives_idle=30 keepalives_interval=5 keepalives_count=3" +
		" tcp_user_timeout=1000 load_balance_hosts=random requirepeer=postgres sslmode=disable" +
		" sslcrl=x sslcrldir=y sslcertmode=allow sslcompression=0 sslkeylogfile=z" +
		" ssl_min_protocol_version=TLSv1.2 ssl_max_protocol_version=TLSv1.3 hostaddr=127.0.0.1" +
		" fallback_application_name=fallback"
	config, err := ParseConfig(settings)
	if err != nil {
		t.Fatalf("ParseConfig = %v", err)
	}
	for key := range config.RuntimeParams {
		if key != "application_name" {
			t.Errorf("RuntimeParams has %q, which the server would refuse", key)
		}
	}
	if config.RuntimeParams["application_name"] != "fallback" {
		t.Errorf("application_name = %q, want fallback_application_name's", config.RuntimeParams["application_name"])
	}
}

func TestParseFallbackApplicationName(t *testing.T) {
	config, err := ParseConfig("host=a user=u application_name=mine fallback_application_name=fallback")
	if err != nil || config.RuntimeParams["application_name"] != "mine" {
		t.Fatalf("application_name = %q, %v; want the one given over the fallback", config.RuntimeParams["application_name"], err)
	}
}

func TestParseHostAddr(t *testing.T) {
	tests := []struct {
		settings string
		hosts    []string
		addrs    []string
		targets  []string
		bad      bool
	}{
		{settings: "host=db.example hostaddr=10.0.0.1", hosts: []string{"db.example"}, addrs: []string{"10.0.0.1"}, targets: []string{"10.0.0.1:5432"}},
		// With no host, the address is the host TLS checks the certificate
		// against.
		{settings: "hostaddr=10.0.0.1,::1 port=5433", hosts: []string{"10.0.0.1", "::1"}, addrs: []string{"10.0.0.1", "::1"}, targets: []string{"10.0.0.1:5433", "[::1]:5433"}},
		// An empty entry dials its host as usual.
		{settings: "host=a,b hostaddr=,10.0.0.2", hosts: []string{"a", "b"}, addrs: []string{"", "10.0.0.2"}, targets: []string{"a:5432", "10.0.0.2:5432"}},
		// A socket directory with an address dials the address.
		{settings: "host=/tmp hostaddr=127.0.0.1", hosts: []string{"/tmp"}, addrs: []string{"127.0.0.1"}, targets: []string{"127.0.0.1:5432"}},
		{settings: "host=a,b hostaddr=10.0.0.1", bad: true},
		{settings: "host=a hostaddr=db.example", bad: true},
	}
	for _, tt := range tests {
		config, err := ParseConfig(tt.settings + " user=u sslmode=disable")
		if tt.bad {
			if !errors.Is(err, ErrInvalidConnString) {
				t.Errorf("%q: error = %v, want ErrInvalidConnString", tt.settings, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", tt.settings, err)
		}
		for i, server := range config.servers() {
			one := config.at(server)
			_, target := one.target()
			if server.Host != tt.hosts[i] || server.HostAddr != tt.addrs[i] || target != tt.targets[i] {
				t.Errorf("%q server %d = %s, %s, dialling %s; want %s, %s, %s", tt.settings, i, server.Host, server.HostAddr, target, tt.hosts[i], tt.addrs[i], tt.targets[i])
			}
		}
	}
}

// hostaddr keeps the host's name for TLS: the certificate is checked
// against db.example, not the address dialled.
func TestHostAddrKeepsTheNameForTLS(t *testing.T) {
	config, err := ParseConfig("host=db.example hostaddr=10.0.0.1 user=u sslmode=verify-full")
	if err != nil {
		t.Fatal(err)
	}
	if config.TLSConfig.ServerName != "db.example" {
		t.Errorf("ServerName = %q, want db.example", config.TLSConfig.ServerName)
	}
}

// keepalives=0 turns probes off, and a zero or missing setting leaves the
// dialer's default, where a zero idle time used to be passed on as zero.
func TestParseKeepAlive(t *testing.T) {
	tests := []struct {
		settings string
		want     net.KeepAliveConfig
		off      bool
		bad      bool
	}{
		{settings: ""},
		{settings: "keepalives=0", off: true},
		{settings: "keepalives=1"},
		{settings: "keepalives_idle=30 keepalives_interval=5 keepalives_count=3", want: net.KeepAliveConfig{Idle: 30 * time.Second, Interval: 5 * time.Second, Count: 3}},
		{settings: "keepalives_idle=0 keepalives_count=0"},
		{settings: "keepalives=yes", bad: true},
		{settings: "keepalives_idle=-1", bad: true},
		{settings: "keepalives_count=x", bad: true},
	}
	for _, tt := range tests {
		config, err := ParseConfig("host=a user=u sslmode=disable " + tt.settings)
		if tt.bad != errors.Is(err, ErrInvalidConnString) {
			t.Errorf("%q: error = %v, bad %v", tt.settings, err, tt.bad)
			continue
		}
		if tt.bad {
			continue
		}
		if config.KeepAlive != tt.want || config.NoKeepAlive != tt.off {
			t.Errorf("%q: KeepAlive = %+v, NoKeepAlive %v, want %+v, %v", tt.settings, config.KeepAlive, config.NoKeepAlive, tt.want, tt.off)
		}
	}
}

func TestParseTransportSettings(t *testing.T) {
	config, err := ParseConfig("host=a user=u sslmode=disable tcp_user_timeout=2500 load_balance_hosts=random requirepeer=postgres")
	if err != nil {
		t.Fatal(err)
	}
	if config.TCPUserTimeout != 2500*time.Millisecond || !config.LoadBalanceHosts || config.RequirePeer != "postgres" {
		t.Errorf("TCPUserTimeout %v, LoadBalanceHosts %v, RequirePeer %q", config.TCPUserTimeout, config.LoadBalanceHosts, config.RequirePeer)
	}
	for _, bad := range []string{"tcp_user_timeout=soon", "load_balance_hosts=round-robin"} {
		if _, err := ParseConfig("host=a user=u " + bad); !errors.Is(err, ErrInvalidConnString) {
			t.Errorf("%q: error = %v, want ErrInvalidConnString", bad, err)
		}
	}
}

// PGOPTIONS and the other variables libpq sends in the startup packet are
// read, as they are by every libpq program.
func TestParseSessionEnvironment(t *testing.T) {
	t.Setenv("PGOPTIONS", "-c work_mem=8MB")
	t.Setenv("PGCLIENTENCODING", "UTF8")
	t.Setenv("PGTZ", "UTC")
	t.Setenv("PGDATESTYLE", "ISO")
	t.Setenv("PGGEQO", "off")
	t.Setenv("PGHOSTADDR", "10.0.0.9")
	config, err := ParseConfig("host=a user=u sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"options": "-c work_mem=8MB", "client_encoding": "UTF8", "timezone": "UTC", "datestyle": "ISO", "geqo": "off"}
	for key, value := range want {
		if config.RuntimeParams[key] != value {
			t.Errorf("RuntimeParams[%q] = %q, want %q", key, config.RuntimeParams[key], value)
		}
	}
	if config.HostAddr != "10.0.0.9" {
		t.Errorf("HostAddr = %q, want PGHOSTADDR's", config.HostAddr)
	}
}

// load_balance_hosts=random tries the servers in every order over enough
// connections, where they used to be tried as listed, always the first
// first.
func TestLoadBalanceHostsShuffles(t *testing.T) {
	config, err := ParseConfig("host=a,b,c user=u sslmode=disable load_balance_hosts=random")
	if err != nil {
		t.Fatal(err)
	}
	firsts := map[string]bool{}
	for range 200 {
		firsts[config.servers()[0].Host] = true
	}
	if len(firsts) != 3 {
		t.Errorf("first servers over 200 tries = %v, want each of a, b and c", firsts)
	}
	config.LoadBalanceHosts = false
	for range 20 {
		if first := config.servers()[0].Host; first != "a" {
			t.Fatalf("without load balancing the first server was %q", first)
		}
	}
}

// With load balancing and no LookupFunc, a host is looked up so that its
// addresses are shuffled too, each tried as a server of its own.
func TestLoadBalanceLooksHostsUp(t *testing.T) {
	config := &Config{Host: "localhost", LoadBalanceHosts: true}
	if config.lookup() == nil {
		t.Fatal("lookup() = nil with LoadBalanceHosts")
	}
	config.LoadBalanceHosts = false
	if config.lookup() != nil {
		t.Fatal("lookup() set without LoadBalanceHosts or LookupFunc")
	}
}

func liveTransport(t *testing.T, extra string) (*Conn, error) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := ParseConfig(dsn + sep(dsn) + extra)
	if err != nil {
		t.Fatalf("ParseConfig = %v", err)
	}
	conn, err := Connect(t.Context(), config)
	if err == nil {
		t.Cleanup(func() { _ = conn.Close(context.Background()) })
	}
	return conn, err
}

// sep joins further settings to a URL's query or a key=value string.
func sep(dsn string) string {
	switch {
	case !strings.Contains(dsn, "://"):
		return " "
	case strings.Contains(dsn, "?"):
		return "&"
	}
	return "?"
}

// hostaddr is dialled instead of the host, which need not resolve at all.
func TestLiveHostAddr(t *testing.T) {
	config := liveConfig(t)
	if config.unix() {
		t.Skip("DATABASE_URL is a socket")
	}
	addr := config.Host
	if addr == "localhost" {
		addr = "127.0.0.1"
	}
	config.Host, config.HostAddr = "no-such-host.invalid", addr
	config.TLSConfig = nil
	conn, err := Connect(t.Context(), config)
	if err != nil {
		t.Fatalf("Connect with hostaddr = %v", err)
	}
	_ = conn.Close(context.Background())
}
