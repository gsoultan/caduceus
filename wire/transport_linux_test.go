// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"errors"
	"os"
	"os/user"
	"strconv"
	"syscall"
	"testing"
)

func itoa(n uint16) string { return strconv.Itoa(int(n)) }

func socketOption(t *testing.T, conn *Conn, level, option int) int {
	t.Helper()
	raw := rawSocket(conn.netConn)
	if raw == nil {
		t.Fatal("no socket")
	}
	var value int
	var optErr error
	if err := raw.Control(func(fd uintptr) { value, optErr = syscall.GetsockoptInt(int(fd), level, option) }); err != nil {
		t.Fatal(err)
	}
	if optErr != nil {
		t.Fatal(optErr)
	}
	return value
}

// The keepalive and user-timeout settings reach the socket.
func TestLiveTransportSocketOptions(t *testing.T) {
	conn, err := liveTransport(t, "keepalives_idle=37&keepalives_interval=7&keepalives_count=4&tcp_user_timeout=4321&sslmode=disable")
	if err != nil {
		t.Fatalf("Connect = %v", err)
	}
	const tcpKeepIdle, tcpKeepIntvl, tcpKeepCnt = 4, 5, 6
	checks := []struct {
		name          string
		level, option int
		want          int
	}{
		{"SO_KEEPALIVE", syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1},
		{"TCP_KEEPIDLE", syscall.IPPROTO_TCP, tcpKeepIdle, 37},
		{"TCP_KEEPINTVL", syscall.IPPROTO_TCP, tcpKeepIntvl, 7},
		{"TCP_KEEPCNT", syscall.IPPROTO_TCP, tcpKeepCnt, 4},
		{"TCP_USER_TIMEOUT", syscall.IPPROTO_TCP, tcpUserTimeoutOption, 4321},
	}
	for _, check := range checks {
		if got := socketOption(t, conn, check.level, check.option); got != check.want {
			t.Errorf("%s = %d, want %d", check.name, got, check.want)
		}
	}
	off, err := liveTransport(t, "keepalives=0&sslmode=disable")
	if err != nil {
		t.Fatalf("Connect = %v", err)
	}
	if got := socketOption(t, off, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE); got != 0 {
		t.Errorf("SO_KEEPALIVE with keepalives=0 = %d, want 0", got)
	}
}

// requirepeer is checked against the socket's peer before anything is sent:
// the right user connects, and the wrong one is refused with ErrRequirePeer
// rather than with whatever the server would say.
func TestLiveRequirePeer(t *testing.T) {
	const dir = "/var/run/postgresql"
	config := liveConfig(t)
	if _, err := os.Stat(dir + "/.s.PGSQL." + itoa(config.Port)); err != nil {
		t.Skip("no local socket at " + dir)
	}
	owner := socketOwner(t, dir+"/.s.PGSQL."+itoa(config.Port))
	config.Host, config.TLSConfig, config.Fallbacks = dir, nil, nil
	config.RequirePeer = "nobody-at-all"
	if _, err := Connect(t.Context(), config); !errors.Is(err, ErrRequirePeer) {
		t.Fatalf("Connect as the wrong peer = %v, want ErrRequirePeer", err)
	}
	config.RequirePeer = owner
	conn, err := Connect(t.Context(), config)
	if errors.Is(err, ErrRequirePeer) {
		t.Fatalf("Connect as the right peer %q = %v", owner, err)
	}
	if err == nil {
		_ = conn.Close(context.Background())
	}
}

func socketOwner(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := user.LookupId(strconv.FormatUint(uint64(info.Sys().(*syscall.Stat_t).Uid), 10))
	if err != nil {
		t.Fatal(err)
	}
	return owner.Username
}
