// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
)

// xorWrapper is a GSSWrapper whose "encryption" is a XOR with 0x5a and a
// marker, enough to tell wrapped bytes from plain ones.
type xorWrapper struct {
	fakeGSS
	wraps int
}

func (w *xorWrapper) Wrap(plain []byte) ([]byte, error) {
	w.wraps++
	return append([]byte("W"), xor(plain)...), nil
}

func (w *xorWrapper) Unwrap(token []byte) ([]byte, error) {
	if len(token) == 0 || token[0] != 'W' {
		return nil, errors.New("not wrapped")
	}
	return xor(token[1:]), nil
}

func xor(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[i] ^ 0x5a
	}
	return out
}

// gssEncServer answers a GSSENCRequest with answer. Having said G, it takes
// the client's token, replies, and then reads the startup message wrapped
// and lets the client in, wrapped. Having said N, it reads the startup
// message in the clear. It sends what it saw on seen.
type gssEncServer struct {
	conn    net.Conn
	answer  byte
	request chan []byte
	startup chan []byte
	done    chan error
}

func serveGSSEnc(conn net.Conn, answer byte) *gssEncServer {
	s := &gssEncServer{conn: conn, answer: answer, request: make(chan []byte, 1), startup: make(chan []byte, 1), done: make(chan error, 1)}
	go func() { s.done <- s.serve() }()
	return s
}

func (s *gssEncServer) serve() error {
	defer close(s.request)
	defer close(s.startup)
	first := make([]byte, 8)
	if _, err := io.ReadFull(s.conn, first); err != nil {
		return err
	}
	s.request <- first
	if binary.BigEndian.Uint32(first[4:]) != gssEncRequestCode {
		return s.plainStartup(first)
	}
	if _, err := s.conn.Write([]byte{s.answer}); err != nil {
		return err
	}
	if s.answer != 'G' {
		return s.plainStartup(nil)
	}
	token, err := readPacket(s.conn)
	if err != nil || string(token) != "first" {
		return errors.Join(err, fmt.Errorf("first token %q", token))
	}
	if err := writePacket(s.conn, []byte("from server")); err != nil {
		return err
	}
	return s.wrappedStartup()
}

// plainStartup reads the startup message in the clear, the first bytes
// already read, and lets the client in.
func (s *gssEncServer) plainStartup(head []byte) error {
	if head == nil {
		head = make([]byte, 8)
		if _, err := io.ReadFull(s.conn, head); err != nil {
			return err
		}
	}
	body := make([]byte, binary.BigEndian.Uint32(head[:4])-uint32(len(head)))
	if _, err := io.ReadFull(s.conn, body); err != nil {
		return err
	}
	s.startup <- append(head, body...)
	_, err := s.conn.Write(append(authRequest(authOK, nil), MsgReadyForQuery, 0, 0, 0, 5, TxIdle))
	return err
}

func (s *gssEncServer) wrappedStartup() error {
	// The provider's last token, which the context is done with, comes
	// first.
	if last, err := readPacket(s.conn); err != nil || string(last) != "second" {
		return errors.Join(err, fmt.Errorf("last token %q", last))
	}
	token, err := readPacket(s.conn)
	if err != nil {
		return err
	}
	if token[0] != 'W' {
		return fmt.Errorf("the startup message was not wrapped: %q", token)
	}
	s.startup <- xor(token[1:])
	reply := append(authRequest(authOK, nil), MsgReadyForQuery, 0, 0, 0, 5, TxIdle)
	return writePacket(s.conn, append([]byte("W"), xor(reply)...))
}

func readPacket(conn net.Conn) ([]byte, error) {
	var length [4]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return nil, err
	}
	token := make([]byte, binary.BigEndian.Uint32(length[:]))
	_, err := io.ReadFull(conn, token)
	return token, err
}

func writePacket(conn net.Conn, token []byte) error {
	_, err := conn.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(token))), token...))
	return err
}

// gssencmode encrypts the connection with the provider's context before
// anything else is sent: the startup message goes wrapped, and so does
// everything after it. It used to be refused for require, and ignored for
// prefer.
func TestGSSEncryption(t *testing.T) {
	for _, mode := range []string{"require", "prefer"} {
		t.Run(mode, func(t *testing.T) {
			var server net.Conn
			config := kerberosConfig(t, "host=db.example user=u sslmode=require require_auth=gss gssencmode="+mode, &server)
			wrapper := &xorWrapper{}
			config.NewGSS = func() (GSS, error) { return wrapper, nil }
			s := serveGSSEnc(server, 'G')
			conn, err := Connect(t.Context(), config)
			if err != nil {
				t.Fatalf("Connect = %v, server: %v", err, <-s.done)
			}
			defer func() { go func() { _, _ = io.Copy(io.Discard, server) }(); _ = conn.Close(context.Background()) }()
			if err := <-s.done; err != nil {
				t.Fatal(err)
			}
			if startup := <-s.startup; !bytes.Contains(startup, []byte("user\x00u\x00")) {
				t.Errorf("startup message %q", startup)
			}
			if wrapper.host != "db.example" || wrapper.service != "postgres" || string(wrapper.continued) != "from server" {
				t.Errorf("provider was asked %+v", wrapper.fakeGSS)
			}
			if _, ok := conn.NetConn().(*gssConn); !ok {
				t.Errorf("the connection is a %T", conn.NetConn())
			}
		})
	}
}

// A server that declines leaves prefer to go on in the clear, here, as
// sslmode=disable has it, and fails require. Where prefer has no provider
// that can encrypt, or one with no credentials, nothing is asked for.
func TestGSSEncryptionDeclined(t *testing.T) {
	tests := []struct {
		name, mode string
		answer     byte
		provider   func() (GSS, error)
		want       error
		asked      bool
	}{
		{"prefer, declined", "prefer", 'N', func() (GSS, error) { return &xorWrapper{}, nil }, nil, true},
		{"require, declined", "require", 'N', func() (GSS, error) { return &xorWrapper{}, nil }, ErrGSSEncRefused, true},
		{"require, a server before 12", "require", 'E', func() (GSS, error) { return &xorWrapper{}, nil }, ErrGSSEncRefused, true},
		{"prefer, a provider that cannot encrypt", "prefer", 'G', func() (GSS, error) { return &fakeGSS{}, nil }, nil, false},
		{"require, a provider that cannot encrypt", "require", 'G', func() (GSS, error) { return &fakeGSS{}, nil }, ErrGSSEncRefused, false},
		{"prefer, no credentials", "prefer", 'G', func() (GSS, error) { return nil, errors.New("no ticket") }, nil, false},
		{"prefer, no provider", "prefer", 'G', nil, nil, false},
		{"require, no provider", "require", 'G', nil, ErrNoGSS, false},
		{"disable", "disable", 'G', func() (GSS, error) { return &xorWrapper{}, nil }, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server net.Conn
			config := kerberosConfig(t, "host=db user=u sslmode=disable gssencmode="+tt.mode, &server)
			config.NewGSS = tt.provider
			s := serveGSSEnc(server, tt.answer)
			conn, err := Connect(t.Context(), config)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("Connect = %v, want %v", err, tt.want)
			}
			if conn != nil {
				go func() { _, _ = io.Copy(io.Discard, server) }()
				_ = conn.Close(context.Background())
			}
			_ = server.Close()
			request, sent := <-s.request
			if asked := sent && binary.BigEndian.Uint32(request[4:]) == gssEncRequestCode; asked != tt.asked {
				t.Errorf("asked for encryption = %v, want %v", asked, tt.asked)
			}
		})
	}
}

// A Unix socket is never encrypted, as in libpq: prefer does not ask, and
// require fails before asking.
func TestGSSEncryptionUnixSocket(t *testing.T) {
	for mode, want := range map[string]error{"prefer": nil, "require": ErrGSSEncRefused} {
		var server net.Conn
		config := kerberosConfig(t, "host=/run/postgresql user=u gssencmode="+mode, &server)
		config.NewGSS = func() (GSS, error) { return &xorWrapper{}, nil }
		s := serveGSSEnc(server, 'G')
		conn, err := Connect(t.Context(), config)
		if !errors.Is(err, want) || (want == nil && err != nil) {
			t.Errorf("%s: Connect = %v, want %v", mode, err, want)
		}
		if conn != nil {
			go func() { _, _ = io.Copy(io.Discard, server) }()
			_ = conn.Close(context.Background())
		}
		_ = server.Close()
		if request, sent := <-s.request; sent && binary.BigEndian.Uint32(request[4:]) == gssEncRequestCode {
			t.Errorf("%s: asked for encryption over a Unix socket", mode)
		}
	}
}

// A token larger than the server could send is refused, rather than read.
func TestGSSEncryptionTokenTooLarge(t *testing.T) {
	var server net.Conn
	config := kerberosConfig(t, "host=db user=u sslmode=disable gssencmode=require", &server)
	config.NewGSS = func() (GSS, error) { return &xorWrapper{}, nil }
	go func() {
		_, _ = io.ReadFull(server, make([]byte, 8))
		_, _ = server.Write([]byte{'G'})
		_, _ = readPacket(server)
		_, _ = server.Write(binary.BigEndian.AppendUint32(nil, gssPacketSize+1))
	}()
	if _, err := Connect(t.Context(), config); !errors.Is(err, ErrProtocol) {
		t.Fatalf("Connect = %v, want ErrProtocol", err)
	}
}

// A server that fails the context sends an ErrorResponse where a token's
// length would be, and the login fails with it, not with a token of some
// enormous length.
func TestGSSEncryptionServerError(t *testing.T) {
	var server net.Conn
	config := kerberosConfig(t, "host=db user=u sslmode=disable gssencmode=require", &server)
	config.NewGSS = func() (GSS, error) { return &xorWrapper{}, nil }
	go func() {
		_, _ = io.ReadFull(server, make([]byte, 8))
		_, _ = server.Write([]byte{'G'})
		_, _ = readPacket(server)
		body := []byte("SFATAL\x00C28000\x00Maccepting GSS security context failed\x00\x00")
		message := append([]byte{'E'}, binary.BigEndian.AppendUint32(nil, uint32(4+len(body)))...)
		_, _ = server.Write(append(message, body...))
	}()
	_, err := Connect(t.Context(), config)
	var serverErr *Error
	if !errors.As(err, &serverErr) || serverErr.Code != "28000" {
		t.Fatalf("Connect = %v, want the server's 28000", err)
	}
}

// What is written goes in packets no larger than the server reads, and what
// is read comes out whole, however the packets split it.
func TestGSSConnPackets(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	conn := &gssConn{Conn: client, wrapper: &xorWrapper{}}
	payload := bytes.Repeat([]byte("0123456789"), 4000)
	go func() { _, _ = conn.Write(payload) }()
	var got []byte
	for len(got) < len(payload) {
		token, err := readPacket(server)
		if err != nil {
			t.Fatal(err)
		}
		if len(token)+4 > gssPacketSize {
			t.Fatalf("a packet of %d bytes", len(token)+4)
		}
		got = append(got, xor(token[1:])...)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("what was written is not what was sent")
	}
	go func() {
		for _, part := range [][]byte{[]byte("hello, "), []byte("world")} {
			_ = writePacket(server, append([]byte("W"), xor(part)...))
		}
	}()
	read := make([]byte, 12)
	if _, err := io.ReadFull(conn, read); err != nil || string(read) != "hello, world" {
		t.Fatalf("read %q, %v", read, err)
	}
	go func() { _ = writePacket(server, []byte("not wrapped")) }()
	if _, err := conn.Read(read); err == nil {
		t.Error("an unwrapped packet was read")
	}
	go func() { _, _ = server.Write([]byte{0xff, 0xff, 0xff, 0xff}) }()
	if _, err := conn.Read(read); !errors.Is(err, ErrProtocol) {
		t.Errorf("a packet of 4 GB = %v, want ErrProtocol", err)
	}
}
