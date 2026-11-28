// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// authority is a test certificate authority.
type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newAuthority(t *testing.T, name string) *authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &authority{cert: cert, key: key}
}

// issue signs a certificate for localhost with the given serial.
func (a *authority) issue(t *testing.T, serial int64, client bool) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	usage := x509.ExtKeyUsageServerAuth
	if client {
		usage = x509.ExtKeyUsageClientAuth
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, ExtKeyUsage: []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// crl writes a revocation list from the authority naming serials, valid
// until next.
func (a *authority) crl(t *testing.T, path string, next time.Time, serials ...int64) {
	t.Helper()
	var entries []x509.RevocationListEntry
	for _, serial := range serials {
		entries = append(entries, x509.RevocationListEntry{SerialNumber: big.NewInt(serial), RevocationTime: time.Now().Add(-time.Minute)})
	}
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(1), ThisUpdate: time.Now().Add(-time.Hour), NextUpdate: next,
		RevokedCertificateEntries: entries,
	}, a.cert, a.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (a *authority) rootFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "root.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.cert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// tlsServer answers one SSLRequest with S, completes the handshake as
// config says, and answers the startup packet with an error that says it
// got that far. It reports the connection state it saw.
func tlsServer(t *testing.T, config *tls.Config) (string, string, <-chan tls.ConnectionState) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	states := make(chan tls.ConnectionState, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request [8]byte
		if _, err := io.ReadFull(conn, request[:]); err != nil {
			return
		}
		_, _ = conn.Write([]byte{'S'})
		secured := tls.Server(conn, config)
		if err := secured.Handshake(); err != nil {
			return
		}
		states <- secured.ConnectionState()
		var length [4]byte
		if _, err := io.ReadFull(secured, length[:]); err != nil {
			return
		}
		body := AppendString(AppendString([]byte{'S'}, "FATAL"), "")
		body = AppendString(append(body, 'C'), "28000")
		body = AppendString(append(body, 'M'), "TLS reached")
		message, mark := StartMessage(nil, MsgErrorResponse)
		_, _ = secured.Write(FinishMessage(append(append(message, body...), 0), mark))
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	return host, port, states
}

func reached(err error) bool {
	var server *Error
	return errors.As(err, &server) && server.Message == "TLS reached"
}

func connectTLS(t *testing.T, server *tls.Config, settings string) error {
	t.Helper()
	_, port, _ := tlsServer(t, server)
	config, err := ParseConfig("host=localhost hostaddr=127.0.0.1 port=" + port + " user=u connect_timeout=5 " + settings)
	if err != nil {
		t.Fatalf("ParseConfig(%q) = %v", settings, err)
	}
	_, err = Connect(context.Background(), config)
	return err
}

// sslcrl and sslcrldir refuse a server whose certificate its authority has
// revoked, in verify-ca and verify-full alike; they used to be sent to the
// server as settings, which refused the connection.
func TestRevocationLists(t *testing.T) {
	dir := t.TempDir()
	ca := newAuthority(t, "test root")
	other := newAuthority(t, "another root")
	root := ca.rootFile(t, dir)
	server := &tls.Config{Certificates: []tls.Certificate{ca.issue(t, 42, false)}}
	revoked, clean, expired, foreign := filepath.Join(dir, "revoked.crl"), filepath.Join(dir, "clean.crl"), filepath.Join(dir, "expired.crl"), filepath.Join(dir, "foreign.crl")
	ca.crl(t, revoked, time.Now().Add(time.Hour), 7, 42)
	ca.crl(t, clean, time.Now().Add(time.Hour), 7)
	ca.crl(t, expired, time.Now().Add(-time.Minute))
	other.crl(t, foreign, time.Now().Add(time.Hour), 42)
	// The same name as the root, another key: a list anyone could write.
	impostor := newAuthority(t, "test root")
	forged := filepath.Join(dir, "forged.crl")
	impostor.crl(t, forged, time.Now().Add(time.Hour))
	crlDir := filepath.Join(dir, "crls")
	if err := os.Mkdir(crlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ca.crl(t, filepath.Join(crlDir, "a.crl"), time.Now().Add(time.Hour), 42)

	for _, mode := range []string{"verify-ca", "verify-full"} {
		base := "sslmode=" + mode + " sslrootcert=" + conninfo(root)
		tests := []struct {
			name, settings string
			revoked        bool
		}{
			{"no list", "", false},
			{"listed", " sslcrl=" + conninfo(revoked), true},
			{"listed in a directory", " sslcrldir=" + conninfo(crlDir), true},
			{"not listed", " sslcrl=" + conninfo(clean), false},
			{"list expired", " sslcrl=" + conninfo(expired), true},
			// A list from another authority covers nothing of this chain,
			// which OpenSSL, and so libpq, refuses.
			{"no list from the issuer", " sslcrl=" + conninfo(foreign), true},
			// A list in the issuer's name it did not sign is no list from it:
			// accepting it would let a forged, empty list clear a revoked
			// certificate.
			{"list not signed by the issuer", " sslcrl=" + conninfo(forged), true},
		}
		for _, tt := range tests {
			err := connectTLS(t, server, base+tt.settings)
			if tt.revoked != errors.Is(err, ErrRevoked) || (!tt.revoked && !reached(err)) {
				t.Errorf("%s, %s: %v, revoked %v", mode, tt.name, err, tt.revoked)
			}
		}
	}
}

func TestParseRevocationListErrors(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.crl")
	if err := os.WriteFile(junk, []byte("-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, settings := range []string{"sslcrl=" + conninfo(filepath.Join(dir, "missing")), "sslcrl=" + conninfo(junk), "sslcrldir=" + conninfo(dir)} {
		if _, err := ParseConfig("host=localhost user=u sslmode=verify-full " + settings); !errors.Is(err, ErrInvalidConnString) {
			t.Errorf("%s: %v, want ErrInvalidConnString", settings, err)
		}
	}
}

// sslcertmode: require fails where the server does not ask for a client
// certificate; disable sends none even when one is configured and asked for.
func TestSSLCertMode(t *testing.T) {
	dir := t.TempDir()
	ca := newAuthority(t, "test root")
	server := ca.issue(t, 1, false)
	clientCertFile, clientKeyFile, _ := selfSigned(t, dir, "")
	client := " sslcert=" + conninfo(clientCertFile) + " sslkey=" + conninfo(clientKeyFile)

	noRequest := &tls.Config{Certificates: []tls.Certificate{server}}
	asks := &tls.Config{Certificates: []tls.Certificate{server}, ClientAuth: tls.RequestClientCert}
	demands := &tls.Config{Certificates: []tls.Certificate{server}, ClientAuth: tls.RequireAnyClientCert}

	if err := connectTLS(t, noRequest, "sslmode=require sslcertmode=require"+client); !errors.Is(err, ErrSSLCertMode) {
		t.Errorf("require, not asked: %v, want ErrSSLCertMode", err)
	}
	if err := connectTLS(t, asks, "sslmode=require sslcertmode=require"+client); !reached(err) {
		t.Errorf("require, asked: %v", err)
	}
	if err := connectTLS(t, demands, "sslmode=require sslcertmode=allow"+client); !reached(err) {
		t.Errorf("allow, demanded: %v", err)
	}
	if err := connectTLS(t, demands, "sslmode=require sslcertmode=disable"+client); err == nil || reached(err) {
		t.Errorf("disable, demanded: %v, want the handshake refused for want of a certificate", err)
	}
	if _, err := ParseConfig("host=a user=u sslmode=require sslcertmode=sometimes"); !errors.Is(err, ErrInvalidConnString) {
		t.Errorf("sslcertmode=sometimes: %v", err)
	}
}

// ssl_min_protocol_version and ssl_max_protocol_version bound the version
// negotiated.
func TestSSLProtocolVersions(t *testing.T) {
	ca := newAuthority(t, "test root")
	server := &tls.Config{Certificates: []tls.Certificate{ca.issue(t, 1, false)}}
	_, port, states := tlsServer(t, server)
	config, err := ParseConfig("host=localhost hostaddr=127.0.0.1 port=" + port + " user=u sslmode=require ssl_max_protocol_version=TLSv1.2")
	if err != nil {
		t.Fatal(err)
	}
	if err := connectErr(config); !reached(err) {
		t.Fatalf("Connect = %v", err)
	}
	if state := <-states; state.Version != tls.VersionTLS12 {
		t.Errorf("version = %x, want TLS 1.2", state.Version)
	}
	only13 := &tls.Config{Certificates: server.Certificates, MinVersion: tls.VersionTLS13}
	if err := connectTLS(t, only13, "sslmode=require ssl_max_protocol_version=TLSv1.2"); err == nil || reached(err) {
		t.Errorf("a TLS 1.3 server with ssl_max_protocol_version=TLSv1.2: %v, want the handshake to fail", err)
	}
	for _, bad := range []string{"ssl_min_protocol_version=SSLv3", "ssl_min_protocol_version=TLSv1.3 ssl_max_protocol_version=TLSv1.2"} {
		if _, err := ParseConfig("host=a user=u sslmode=require " + bad); !errors.Is(err, ErrInvalidConnString) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	config, err = ParseConfig("host=a user=u sslmode=require")
	if err != nil || config.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("default minimum = %x, %v; want TLS 1.2 as in libpq", config.TLSConfig.MinVersion, err)
	}
}

func connectErr(config *Config) error {
	_, err := Connect(context.Background(), config)
	return err
}

// sslkeylogfile receives the session's keys in the NSS format. Reading the
// connection string creates nothing: the file is opened when a connection
// is.
func TestSSLKeyLogFile(t *testing.T) {
	ca := newAuthority(t, "test root")
	path := filepath.Join(t.TempDir(), "keys.log")
	if _, err := ParseConfig("host=a user=u sslmode=require sslkeylogfile=" + conninfo(path)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ParseConfig created the key log: %v", err)
	}
	if err := connectTLS(t, &tls.Config{Certificates: []tls.Certificate{ca.issue(t, 1, false)}}, "sslmode=require sslkeylogfile="+conninfo(path)); !reached(err) {
		t.Fatalf("Connect = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		t.Fatalf("key log = %q, %v; want the session's keys", data, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("key log mode = %v, want 0600: it is a secret", info.Mode().Perm())
	}
}

// conninfo quotes a path as a key=value connection string has it: a
// backslash, such as Windows separates directories with, escapes the
// character after it, as in libpq.
func conninfo(path string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(path, `\`, `\\`), "'", `\'`) + "'"
}
