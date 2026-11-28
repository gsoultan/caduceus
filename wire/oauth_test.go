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
	"strings"
	"testing"
)

// oauthServer offers mechanisms, reads the client's SASLInitialResponse,
// and answers it with refusal, if it is not empty, or lets the client in. It
// sends what it read on mechanism and response, and what went wrong on its
// side on done.
type oauthServer struct {
	mechanism, response chan string
	done                chan error
}

func serveOAuth(server net.Conn, mechanisms, refusal string) *oauthServer {
	s := &oauthServer{mechanism: make(chan string, 1), response: make(chan string, 1), done: make(chan error, 1)}
	go func() { s.done <- s.serve(server, mechanisms, refusal) }()
	return s
}

func (s *oauthServer) serve(server net.Conn, mechanisms, refusal string) error {
	defer close(s.mechanism)
	defer close(s.response)
	var length [4]byte
	if _, err := io.ReadFull(server, length[:]); err != nil {
		return err
	}
	if _, err := io.ReadFull(server, make([]byte, binary.BigEndian.Uint32(length[:])-4)); err != nil {
		return err
	}
	if _, err := server.Write(authRequest(authSASL, []byte(mechanisms))); err != nil {
		return err
	}
	body, err := readGSSResponse(server)
	if err != nil {
		return err
	}
	name, rest, _ := bytes.Cut(body, []byte{0})
	s.mechanism <- string(name)
	if len(rest) < 4 || int(binary.BigEndian.Uint32(rest)) != len(rest)-4 {
		return fmt.Errorf("initial response %q", rest)
	}
	s.response <- string(rest[4:])
	if refusal == "" {
		_, err := server.Write(append(authRequest(authOK, nil), MsgReadyForQuery, 0, 0, 0, 5, TxIdle))
		return err
	}
	if _, err := server.Write(authRequest(authSASLContinue, []byte(refusal))); err != nil {
		return err
	}
	if end, err := readGSSResponse(server); err != nil || string(end) != "\x01" {
		return errors.Join(err, fmt.Errorf("the client ended the exchange with %q", end))
	}
	return nil
}

// A server asking for OAuth, as PostgreSQL 18 does, gets the token the hook
// gets, asked with the connection's oauth_* settings, in RFC 7628's initial
// response. It used to be refused as an unknown SASL mechanism.
func TestOAuthLogin(t *testing.T) {
	var server net.Conn
	config := kerberosConfig(t, "host=db user=u sslmode=disable require_auth=oauth oauth_issuer=https://issuer.example oauth_client_id=app oauth_client_secret=s3cret oauth_scope='openid pg'", &server)
	type key struct{}
	ctx := context.WithValue(t.Context(), key{}, "the caller's")
	var asked OAuthRequest
	config.OAuthToken = func(ctx context.Context, request OAuthRequest) (string, error) {
		if ctx.Value(key{}) != "the caller's" {
			t.Error("the hook was not given the caller's context")
		}
		asked = request
		return "eyJ0.payload-sig_~+/==", nil
	}
	s := serveOAuth(server, "OAUTHBEARER\x00\x00", "")
	conn, err := Connect(ctx, config)
	if err != nil {
		t.Fatalf("Connect = %v, server: %v", err, <-s.done)
	}
	go func() { _, _ = io.Copy(io.Discard, server) }()
	_ = conn.Close(context.Background())
	if err := <-s.done; err != nil {
		t.Fatal(err)
	}
	want := OAuthRequest{Issuer: "https://issuer.example", ClientID: "app", ClientSecret: "s3cret", Scope: "openid pg"}
	if asked != want {
		t.Errorf("the hook was asked %+v, want %+v", asked, want)
	}
	if mechanism := <-s.mechanism; mechanism != "OAUTHBEARER" {
		t.Errorf("mechanism = %q", mechanism)
	}
	if response := <-s.response; response != "n,,\x01auth=Bearer eyJ0.payload-sig_~+/==\x01\x01" {
		t.Errorf("initial response = %q", response)
	}
}

// A token the server refuses ends the exchange as RFC 7628 has it, with a
// single separator, and the error says what the server said of the token,
// so that a scope or an issuer configured wrong can be put right.
func TestOAuthTokenRefused(t *testing.T) {
	var server net.Conn
	config := kerberosConfig(t, "host=db user=u sslmode=disable", &server)
	config.OAuthToken = func(context.Context, OAuthRequest) (string, error) { return "expired", nil }
	s := serveOAuth(server, "OAUTHBEARER\x00\x00", `{"status":"invalid_token","scope":"openid pg","openid-configuration":"https://issuer.example/.well-known/openid-configuration"}`)
	_, err := Connect(t.Context(), config)
	if !errors.Is(err, ErrOAuth) || !strings.Contains(err.Error(), "invalid_token") || !strings.Contains(err.Error(), "openid pg") || !strings.Contains(err.Error(), "issuer.example") {
		t.Fatalf("Connect = %v, want ErrOAuth with the server's reason", err)
	}
	if strings.Contains(err.Error(), "expired") {
		t.Errorf("the error repeats the token: %v", err)
	}
	if err := <-s.done; err != nil {
		t.Fatal(err)
	}
}

// OAuth is refused before a token is asked for where there is no hook, where
// require_auth does not allow it, and where channel_binding=require asks for
// a binding it cannot give; and a token the hook could not get, or one no
// bearer header can carry, is not sent.
func TestOAuthLoginRefused(t *testing.T) {
	tests := []struct {
		name, dsn string
		hook      bool
		token     string
		hookErr   error
		want      error
		wantAsked bool
	}{
		{"no hook", "host=db user=u sslmode=disable", false, "", nil, ErrOAuth, false},
		{"require_auth", "host=db user=u sslmode=disable require_auth=scram-sha-256", true, "tok3n", nil, ErrRequireAuth, false},
		{"require_auth negated", "host=db user=u sslmode=disable require_auth=!oauth", true, "tok3n", nil, ErrRequireAuth, false},
		{"channel_binding", "host=db user=u sslmode=disable channel_binding=require", true, "tok3n", nil, ErrChannelBinding, false},
		{"hook fails", "host=db user=u sslmode=disable", true, "", context.DeadlineExceeded, context.DeadlineExceeded, true},
		{"a separator in the token", "host=db user=u sslmode=disable", true, "sec ret\x01", nil, ErrOAuth, true},
		{"a space in the token", "host=db user=u sslmode=disable", true, "sec ret", nil, ErrOAuth, true},
		{"empty token", "host=db user=u sslmode=disable", true, "", nil, ErrOAuth, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server net.Conn
			config := kerberosConfig(t, tt.dsn, &server)
			asked := false
			if tt.hook {
				config.OAuthToken = func(context.Context, OAuthRequest) (string, error) {
					asked = true
					return tt.token, tt.hookErr
				}
			}
			s := serveOAuth(server, "OAUTHBEARER\x00\x00", "")
			_, err := Connect(t.Context(), config)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Connect = %v, want %v", err, tt.want)
			}
			if asked != tt.wantAsked {
				t.Errorf("hook asked = %v, want %v", asked, tt.wantAsked)
			}
			if tt.token != "" && strings.Contains(err.Error(), tt.token) {
				t.Errorf("the error repeats the token: %v", err)
			}
			if _, sent := <-s.response; sent {
				t.Error("a response was sent for a login refused")
			}
		})
	}
}

// A server that offers SCRAM and OAuth both gets SCRAM, with the password
// the connection has, and no token is asked for.
func TestOAuthNotPreferredToSCRAM(t *testing.T) {
	for _, offer := range []string{"SCRAM-SHA-256\x00OAUTHBEARER\x00\x00", "OAUTHBEARER\x00SCRAM-SHA-256\x00\x00"} {
		var server net.Conn
		config := kerberosConfig(t, "host=db user=u password=p sslmode=disable", &server)
		config.OAuthToken = func(context.Context, OAuthRequest) (string, error) {
			t.Error("a token was asked for")
			return "t", nil
		}
		s := serveOAuth(server, offer, "")
		go func() { _, _ = Connect(t.Context(), config) }()
		if mechanism := <-s.mechanism; mechanism != "SCRAM-SHA-256" {
			t.Errorf("offered %q, the client chose %q", offer, mechanism)
		}
		_ = server.Close()
	}
}
