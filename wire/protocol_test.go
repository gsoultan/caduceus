// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// startupServer is a server that answers each connection's startup packet
// with what answer returns for the version asked for, and records the
// versions asked for and the cancel requests received.
type startupServer struct {
	listener net.Listener
	asked    chan ProtocolVersion
	cancels  chan []byte
}

func newStartupServer(t *testing.T, answer func(asked ProtocolVersion, attempt int) []byte) *startupServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	s := &startupServer{listener: listener, asked: make(chan ProtocolVersion, 16), cancels: make(chan []byte, 4)}
	go func() {
		for attempt := 0; ; {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var header [8]byte
			if _, err := io.ReadFull(conn, header[:]); err != nil {
				_ = conn.Close()
				continue
			}
			rest := make([]byte, binary.BigEndian.Uint32(header[:4])-8)
			_, _ = io.ReadFull(conn, rest)
			code := binary.BigEndian.Uint32(header[4:])
			if code == cancelRequestCode {
				s.cancels <- rest
				_ = conn.Close()
				continue
			}
			s.asked <- ProtocolVersion(code)
			_, _ = conn.Write(answer(ProtocolVersion(code), attempt))
			attempt++
			// Held open for the client to close, as a server would.
			go func() { _, _ = io.Copy(io.Discard, conn); _ = conn.Close() }()
		}
	}()
	return s
}

func (s *startupServer) config(t *testing.T, extra string) *Config {
	t.Helper()
	port := s.listener.Addr().(*net.TCPAddr).Port
	config, err := ParseConfig("postgres://u:p@127.0.0.1:" + strconv.Itoa(port) + "/db?sslmode=disable" + extra)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

// versionsAsked drains the versions asked for so far.
func (s *startupServer) versionsAsked() []ProtocolVersion {
	var asked []ProtocolVersion
	for {
		select {
		case v := <-s.asked:
			asked = append(asked, v)
		default:
			return asked
		}
	}
}

func message(kind byte, body ...[]byte) []byte {
	joined := bytes.Join(body, nil)
	out := append([]byte{kind}, binary.BigEndian.AppendUint32(nil, uint32(len(joined)+4))...)
	return append(out, joined...)
}

func be32bytes(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }

// loggedIn is a login that needs no password, with a cancel key of keyLen
// bytes.
func loggedIn(keyLen int) []byte {
	key := append(be32bytes(4242), bytes.Repeat([]byte{7}, keyLen)...)
	return bytes.Join([][]byte{
		message(MsgAuthentication, be32bytes(authOK)),
		message(MsgBackendKeyData, key),
		message(MsgReadyForQuery, []byte{TxIdle}),
	}, nil)
}

// negotiation is a NegotiateProtocolVersion offering minor version minor.
func negotiation(minor uint32) []byte {
	return message(MsgNegotiateProtocol, be32bytes(minor), be32bytes(0))
}

func refusal(code string) []byte {
	return message(MsgErrorResponse, []byte("SFATAL\x00C"+code+"\x00Mbad packet header\x00\x00"))
}

// PostgreSQL 18 and later speak 3.2: the connection asks for it, the server
// takes it, and its cancel key, 32 bytes where 3.0's is 4, goes whole into a
// cancel request.
func TestConnectSpeaks32WithAServerThatDoes(t *testing.T) {
	server := newStartupServer(t, func(ProtocolVersion, int) []byte { return loggedIn(32) })
	conn, err := Connect(t.Context(), server.config(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(t.Context())
	if got := server.versionsAsked(); len(got) != 1 || got[0] != Protocol32 {
		t.Fatalf("asked for %v, want 3.2", got)
	}
	if conn.ProtocolVersion() != Protocol32 {
		t.Fatalf("speaks %s, want 3.2", conn.ProtocolVersion())
	}
	if err := conn.CancelRequest(t.Context()); err != nil {
		t.Fatal(err)
	}
	if request := <-server.cancels; len(request) != 4+32 || !bytes.Equal(request[4:], bytes.Repeat([]byte{7}, 32)) {
		t.Fatalf("cancel request carried %d bytes after its code, want the process ID and the 32-byte key", len(request))
	}
}

// A server before PostgreSQL 18 answers 3.2 with the newest it speaks, and
// the connection speaks that.
func TestConnectSpeaksWhatAnOlderServerOffers(t *testing.T) {
	server := newStartupServer(t, func(ProtocolVersion, int) []byte {
		return append(negotiation(0), loggedIn(4)...)
	})
	conn, err := Connect(t.Context(), server.config(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(t.Context())
	if conn.ProtocolVersion() != Protocol30 {
		t.Fatalf("speaks %s, want 3.0", conn.ProtocolVersion())
	}
	if got := server.versionsAsked(); len(got) != 1 {
		t.Fatalf("asked %d times, want once: an offer needs no second dial", len(got))
	}
}

// A pooler that knows only 3.0 refuses a startup packet asking for 3.2, as
// PgBouncer 1.22 does with "bad packet header", and a server older than the
// NegotiateProtocolVersion message with 0A000. The connection asks again for
// 3.0, and the connections after it from the same Config ask for 3.0 at once.
func TestConnectAsksAgainFor30WhenRefused(t *testing.T) {
	for _, code := range []string{"08P01", "0A000"} {
		t.Run(code, func(t *testing.T) {
			server := newStartupServer(t, func(asked ProtocolVersion, _ int) []byte {
				if asked != Protocol30 {
					return refusal(code)
				}
				return loggedIn(4)
			})
			config := server.config(t, "")
			for round, want := range [][]ProtocolVersion{{Protocol32, Protocol30}, {Protocol30}} {
				conn, err := Connect(t.Context(), config.Copy())
				if err != nil {
					t.Fatalf("round %d: %v", round, err)
				}
				_ = conn.Close(t.Context())
				if conn.ProtocolVersion() != Protocol30 {
					t.Fatalf("round %d: speaks %s", round, conn.ProtocolVersion())
				}
				if got := server.versionsAsked(); !equalVersions(got, want) {
					t.Fatalf("round %d: asked for %v, want %v", round, got, want)
				}
			}
		})
	}
}

// An error that is not about the version is not asked about again: a login
// refused by pg_hba.conf is refused whatever version is asked for.
func TestConnectDoesNotAskAgainForAnotherRefusal(t *testing.T) {
	server := newStartupServer(t, func(ProtocolVersion, int) []byte { return refusal("28000") })
	_, err := Connect(t.Context(), server.config(t, ""))
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "28000" {
		t.Fatalf("Connect = %v, want the server's 28000", err)
	}
	if got := server.versionsAsked(); len(got) != 1 {
		t.Fatalf("asked %d times, want once", len(got))
	}
}

// A pooler answers 08P01 for more than the version: a database it does not
// know, or its own shutdown. This used to make the Config ask for 3.0 for
// the rest of the process after one such error, though the pooler spoke 3.2.
// The fallback is now remembered only when asking for 3.0 succeeds.
func TestConnectRemembersOnlyAFallbackThatWorked(t *testing.T) {
	server := newStartupServer(t, func(ProtocolVersion, int) []byte { return refusal("08P01") })
	config := server.config(t, "")
	if _, err := Connect(t.Context(), config.Copy()); err == nil {
		t.Fatal("Connect succeeded")
	}
	if got := config.firstProtocol(); got != Protocol32 {
		t.Fatalf("after a refusal of both versions, asks first for %s, want 3.2", got)
	}
}

// A fallback used to last for the life of the Config, so a pooler upgraded
// to speak 3.2, or a failover to PostgreSQL 18, was never asked for 3.2
// until the process restarted. It now lapses after protocolRetryInterval.
func TestConnectAsksFor32AgainOnceTheFallbackLapses(t *testing.T) {
	var speaks32 atomic.Bool
	server := newStartupServer(t, func(asked ProtocolVersion, _ int) []byte {
		if asked != Protocol30 && !speaks32.Load() {
			return refusal("08P01")
		}
		return loggedIn(4)
	})
	config := server.config(t, "")
	conn, err := Connect(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(t.Context())
	if got := config.firstProtocol(); got != Protocol30 {
		t.Fatalf("after a fallback, asks first for %s, want 3.0", got)
	}
	server.versionsAsked()
	speaks32.Store(true)
	config.downgraded.Store(time.Now().Add(-protocolRetryInterval).UnixNano())
	conn, err = Connect(t.Context(), config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(t.Context())
	if got := server.versionsAsked(); !equalVersions(got, []ProtocolVersion{Protocol32}) {
		t.Fatalf("after the fallback lapsed, asked for %v, want 3.2", got)
	}
}

// min_protocol_version and max_protocol_version bound what is asked for and
// what is accepted.
func TestConnectHonoursTheProtocolBounds(t *testing.T) {
	older := func(ProtocolVersion, int) []byte { return append(negotiation(0), loggedIn(4)...) }
	refusing := func(asked ProtocolVersion, _ int) []byte {
		if asked != Protocol30 {
			return refusal("08P01")
		}
		return loggedIn(4)
	}

	t.Run("max 3.0 asks for 3.0", func(t *testing.T) {
		server := newStartupServer(t, func(ProtocolVersion, int) []byte { return loggedIn(4) })
		conn, err := Connect(t.Context(), server.config(t, "&max_protocol_version=3.0"))
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close(t.Context())
		if got := server.versionsAsked(); !equalVersions(got, []ProtocolVersion{Protocol30}) {
			t.Fatalf("asked for %v, want 3.0", got)
		}
	})
	t.Run("min 3.2 refuses an older server", func(t *testing.T) {
		server := newStartupServer(t, older)
		if _, err := Connect(t.Context(), server.config(t, "&min_protocol_version=3.2")); !errors.Is(err, ErrProtocolVersion) {
			t.Fatalf("Connect = %v, want ErrProtocolVersion", err)
		}
	})
	// This used to return the pooler's bare 08P01, so a caller testing for
	// ErrProtocolVersion missed a pooler that refused 3.2 outright, though
	// it caught a server that offered 3.0.
	t.Run("min 3.2 does not ask again", func(t *testing.T) {
		server := newStartupServer(t, refusing)
		_, err := Connect(t.Context(), server.config(t, "&min_protocol_version=latest"))
		if !errors.Is(err, ErrProtocolVersion) {
			t.Fatalf("Connect = %v, want ErrProtocolVersion", err)
		}
		var failure *Error
		if !errors.As(err, &failure) || failure.Code != "08P01" {
			t.Fatalf("Connect = %v, want the pooler's 08P01 within", err)
		}
		if got := server.versionsAsked(); len(got) != 1 {
			t.Fatalf("asked %d times, want once", len(got))
		}
	})
}

// What the server says of the version is held to the protocol.
func TestConnectRefusesAMalformedStartup(t *testing.T) {
	cases := map[string][]byte{
		"an offer of the version asked for":   append(negotiation(2), loggedIn(4)...),
		"a 32-byte key in protocol 3.0":       append(negotiation(0), loggedIn(32)...),
		"a key longer than 256 bytes":         loggedIn(257),
		"a key shorter than 4 bytes":          loggedIn(2),
		"a truncated NegotiateProtocol":       message(MsgNegotiateProtocol, be32bytes(0)),
		"a refusal after the login has begun": append(message(MsgAuthentication, be32bytes(authOK)), refusal("08P01")...),
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			server := newStartupServer(t, func(ProtocolVersion, int) []byte { return answer })
			if conn, err := Connect(t.Context(), server.config(t, "")); err == nil {
				_ = conn.Close(t.Context())
				t.Fatal("Connect succeeded")
			}
			if got := server.versionsAsked(); len(got) != 1 {
				t.Fatalf("asked %d times, want once", len(got))
			}
		})
	}
}

func TestParseProtocolVersions(t *testing.T) {
	for _, bad := range []string{"min_protocol_version=3.1", "max_protocol_version=4", "min_protocol_version=3.2&max_protocol_version=3.0"} {
		if _, err := ParseConfig("postgres://u@h/db?" + bad); !errors.Is(err, ErrInvalidConnString) {
			t.Errorf("%s = %v, want ErrInvalidConnString", bad, err)
		}
	}
	config, err := ParseConfig("postgres://u@h/db?min_protocol_version=3.0&max_protocol_version=latest")
	if err != nil || config.MinProtocolVersion != Protocol30 || config.MaxProtocolVersion != Protocol32 {
		t.Fatalf("%+v, %v", config, err)
	}
	if _, sent := config.RuntimeParams["max_protocol_version"]; sent {
		t.Error("max_protocol_version would be sent to the server as a setting")
	}
	if Protocol32.String() != "3.2" {
		t.Errorf("Protocol32 prints as %s", Protocol32)
	}
}

func equalVersions(a, b []ProtocolVersion) bool {
	return len(a) == len(b) && (len(a) == 0 || bytes.Equal(versionBytes(a), versionBytes(b)))
}

func versionBytes(vs []ProtocolVersion) []byte {
	var out []byte
	for _, v := range vs {
		out = binary.BigEndian.AppendUint32(out, uint32(v))
	}
	return out
}
