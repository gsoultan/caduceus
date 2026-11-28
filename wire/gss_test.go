// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
)

// fakeGSS records what it is asked and answers with fixed tokens.
type fakeGSS struct {
	host, service, spn string
	continued          []byte
}

func (f *fakeGSS) GetInitToken(host, service string) ([]byte, error) {
	f.host, f.service = host, service
	return []byte("first"), nil
}

func (f *fakeGSS) GetInitTokenFromSPN(spn string) ([]byte, error) {
	f.spn = spn
	return []byte("first"), nil
}

func (f *fakeGSS) Continue(in []byte) (bool, []byte, error) {
	f.continued = append([]byte(nil), in...)
	return true, []byte("second"), nil
}

// kerberosServer is a server that asks for request (gss or sspi), expects the
// client's first token, sends its own, expects the reply, and lets it in. It
// reports what went wrong on its side, or nil.
func kerberosServer(server net.Conn, request uint32) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- func() error {
			var length [4]byte
			if _, err := io.ReadFull(server, length[:]); err != nil {
				return err
			}
			if _, err := io.ReadFull(server, make([]byte, binary.BigEndian.Uint32(length[:])-4)); err != nil {
				return err
			}
			if _, err := server.Write(authRequest(request, nil)); err != nil {
				return err
			}
			if token, err := readGSSResponse(server); err != nil || string(token) != "first" {
				return errors.Join(err, errors.New("first token "+string(token)))
			}
			if _, err := server.Write(authRequest(authGSSContinue, []byte("from server"))); err != nil {
				return err
			}
			if token, err := readGSSResponse(server); err != nil || string(token) != "second" {
				return errors.Join(err, errors.New("second token "+string(token)))
			}
			_, err := server.Write(append(authRequest(authOK, nil), MsgReadyForQuery, 0, 0, 0, 5, TxIdle))
			return err
		}()
	}()
	return done
}

func authRequest(code uint32, data []byte) []byte {
	message := []byte{'R', 0, 0, 0, 0}
	binary.BigEndian.PutUint32(message[1:], uint32(8+len(data)))
	return append(binary.BigEndian.AppendUint32(message, code), data...)
}

func readGSSResponse(server net.Conn) ([]byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(server, header[:]); err != nil {
		return nil, err
	}
	if header[0] != 'p' {
		return nil, errors.New("not a GSSResponse")
	}
	body := make([]byte, binary.BigEndian.Uint32(header[1:])-4)
	_, err := io.ReadFull(server, body)
	return body, err
}

func kerberosConfig(t *testing.T, dsn string, server *net.Conn) *Config {
	t.Helper()
	config, err := ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	client, serverEnd := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = serverEnd.Close() })
	*server = serverEnd
	config.DialFunc = func(context.Context, string, string) (net.Conn, error) { return client, nil }
	return config
}

// A server asking for Kerberos gets the provider's tokens, for the
// principal postgres/host, krbsrvname/host, or krbspn as it is, and gss and
// sspi are answered alike. It used to be refused as an unknown method.
func TestKerberosLogin(t *testing.T) {
	tests := []struct {
		name, dsn         string
		request           uint32
		host, service, sn string
	}{
		{"gss", "host=db.example user=u sslmode=disable", authGSS, "db.example", "postgres", ""},
		{"sspi", "host=db.example user=u sslmode=disable", authSSPI, "db.example", "postgres", ""},
		{"krbsrvname", "host=db.example user=u sslmode=disable krbsrvname=pg", authGSS, "db.example", "pg", ""},
		{"krbspn", "host=db.example user=u sslmode=disable krbspn=pg/other@REALM", authGSS, "", "", "pg/other@REALM"},
		{"socket", "host=/run/postgresql user=u sslmode=disable", authGSS, "localhost", "postgres", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server net.Conn
			config := kerberosConfig(t, tt.dsn, &server)
			gss := &fakeGSS{}
			config.NewGSS = func() (GSS, error) { return gss, nil }
			done := kerberosServer(server, tt.request)
			conn, err := Connect(t.Context(), config)
			if err != nil {
				t.Fatalf("Connect = %v, server: %v", err, <-done)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			go func() { _, _ = io.Copy(io.Discard, server) }()
			_ = conn.Close(context.Background())
			if gss.host != tt.host || gss.service != tt.service || gss.spn != tt.sn || string(gss.continued) != "from server" {
				t.Errorf("provider was asked %+v", gss)
			}
		})
	}
}

// Kerberos is refused before a token is made: with no provider, where
// require_auth does not allow it, and where channel_binding=require asks for
// a binding it cannot give.
func TestKerberosLoginRefused(t *testing.T) {
	tests := []struct {
		name, dsn string
		provider  bool
		want      error
	}{
		{"no provider", "host=db user=u sslmode=disable", false, ErrNoGSS},
		{"require_auth", "host=db user=u sslmode=disable require_auth=scram-sha-256", true, ErrRequireAuth},
		{"channel_binding", "host=db user=u sslmode=disable channel_binding=require", true, ErrChannelBinding},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server net.Conn
			config := kerberosConfig(t, tt.dsn, &server)
			asked := false
			if tt.provider {
				config.NewGSS = func() (GSS, error) { asked = true; return &fakeGSS{}, nil }
			}
			go kerberosServer(server, authGSS)
			_, err := Connect(t.Context(), config)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Connect = %v, want %v", err, tt.want)
			}
			if asked {
				t.Error("a provider was made for a login refused")
			}
		})
	}
	if _, err := ParseConfig("host=db user=u gssencmode=always"); !errors.Is(err, ErrInvalidConnString) {
		t.Errorf("gssencmode=always = %v, want ErrInvalidConnString", err)
	}
	config, err := ParseConfig("host=db user=u gssencmode=prefer krbsrvname=pg gsslib=gssapi")
	if err != nil || config.KerberosServiceName != "pg" || config.GSSEncMode != "prefer" || config.RuntimeParams["gssencmode"] != "" || config.RuntimeParams["gsslib"] != "" {
		t.Errorf("config = %+v, %v", config, err)
	}
}
