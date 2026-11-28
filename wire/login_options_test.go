// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// require_auth is read as libpq reads it, and refuses what libpq refuses.
func TestParseRequireAuth(t *testing.T) {
	allowed := map[string][]string{
		"scram-sha-256":     {"scram-sha-256"},
		"md5,scram-sha-256": {"md5", "scram-sha-256"},
		"!password":         {"md5", "scram-sha-256", "none", "gss"},
		"!none":             {"password", "md5", "scram-sha-256"},
		"none":              {"none"},
	}
	all := []string{"password", "md5", "scram-sha-256", "gss", "sspi", "none"}
	for text, want := range allowed {
		policy, err := parseRequireAuth(text)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		for _, method := range all {
			wanted := strings.Contains(","+strings.Join(want, ",")+",", ","+method+",")
			if text == "!password" {
				wanted = method != "password"
			}
			if text == "!none" {
				wanted = method != "none"
			}
			if policy.allows(method) != wanted {
				t.Errorf("%s: allows(%s) = %v", text, method, !wanted)
			}
		}
	}
	for _, bad := range []string{"md5,!password", "kerberos", "md5,md5"} {
		if _, err := ParseConfig("host=a user=u require_auth=" + bad); !errors.Is(err, ErrInvalidConnString) {
			t.Errorf("require_auth=%s = %v, want ErrInvalidConnString", bad, err)
		}
	}
	if _, err := ParseConfig("host=a user=u channel_binding=always"); !errors.Is(err, ErrInvalidConnString) {
		t.Errorf("channel_binding=always = %v, want ErrInvalidConnString", err)
	}
	config, err := ParseConfig("host=a user=u require_auth=md5 channel_binding=require")
	if err != nil || config.RequireAuth != "md5" || config.ChannelBinding != "require" {
		t.Fatalf("config = %+v, %v", config, err)
	}
}

// sslnegotiation=direct needs TLS to be required, since a server that
// declined it would otherwise be retried in plaintext.
func TestParseSSLNegotiation(t *testing.T) {
	if config, err := ParseConfig("host=a user=u sslmode=require sslnegotiation=direct"); err != nil || !config.DirectTLS {
		t.Fatalf("direct with require: %v", err)
	}
	for _, bad := range []string{"sslmode=prefer sslnegotiation=direct", "sslnegotiation=direct", "sslmode=require sslnegotiation=fast"} {
		if _, err := ParseConfig("host=a user=u " + bad); !errors.Is(err, ErrInvalidConnString) {
			t.Errorf("%s = %v, want ErrInvalidConnString", bad, err)
		}
	}
}

// A service fills in what the connection string leaves out, ahead of the
// environment, from the user's file and then the system's.
func TestParseService(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.conf")
	system := filepath.Join(dir, "pg_service.conf")
	write := func(path, text string) {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(user, "# mine\n[app]\nhost=apphost\nport = 6000\ndbname=appdb\n\n[other]\nhost=otherhost\n")
	write(system, "[shared]\nhost=sharedhost\nuser=shareduser\n")
	t.Setenv("PGSERVICEFILE", user)
	t.Setenv("PGSYSCONFDIR", dir)
	t.Setenv("PGDATABASE", "fromenv")
	t.Setenv("PGSERVICE", "")

	config, err := ParseConfig("service=app user=u port=7000")
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "apphost" || config.Port != 7000 || config.Database != "appdb" {
		t.Errorf("service app: %s:%d/%s, want apphost:7000/appdb", config.Host, config.Port, config.Database)
	}
	t.Setenv("PGSERVICE", "shared")
	if config, err = ParseConfig("sslmode=disable"); err != nil || config.Host != "sharedhost" || config.User != "shareduser" {
		t.Errorf("PGSERVICE=shared from the system file: %v, %v", config, err)
	}
	if _, err := ParseConfig("service=missing user=u"); !errors.Is(err, ErrInvalidConnString) {
		t.Errorf("an undefined service = %v, want ErrInvalidConnString", err)
	}
	write(user, "[app]\nservice=loop\n")
	if _, err := ParseConfig("service=app user=u"); !errors.Is(err, ErrInvalidConnString) {
		t.Errorf("a service naming a service = %v, want ErrInvalidConnString", err)
	}
}

// selfSigned makes a certificate and its key, the key encrypted with
// password in OpenSSL's traditional format if password is not empty.
func selfSigned(t *testing.T, dir, password string) (string, string, tls.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "caduceus"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, SignatureAlgorithm: x509.SHA256WithRSA,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	pair, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(block))
	if err != nil {
		t.Fatal(err)
	}
	if password != "" {
		//nolint:staticcheck // the traditional format is what sslpassword decrypts
		if block, err = x509.EncryptPEMBlock(rand.Reader, block.Type, block.Bytes, []byte(password), x509.PEMCipherAES256); err != nil {
			t.Fatal(err)
		}
	}
	certFile, keyFile := filepath.Join(dir, "client.crt"), filepath.Join(dir, "client.key")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, pair
}

// sslpassword decrypts an encrypted client key; without it, or with the
// wrong one, the key is refused without saying what it was.
func TestParseSSLPassword(t *testing.T) {
	certFile, keyFile, _ := selfSigned(t, t.TempDir(), "key secret")
	base := "host=a user=u sslmode=require sslcert=" + conninfo(certFile) + " sslkey=" + conninfo(keyFile)
	config, err := ParseConfig(base + " sslpassword='key secret'")
	if err != nil || len(config.TLSConfig.Certificates) != 1 {
		t.Fatalf("with sslpassword: %v", err)
	}
	for _, bad := range []string{"", " sslpassword=wrong"} {
		_, err := ParseConfig(base + bad)
		if !errors.Is(err, ErrInvalidConnString) || strings.Contains(err.Error(), "wrong") {
			t.Errorf("sslpassword %q = %v", bad, err)
		}
	}
}

// tls-server-end-point is the hash of the server's certificate with the hash
// its signature uses.
func TestEndPoint(t *testing.T) {
	_, _, pair := selfSigned(t, t.TempDir(), "")
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(certificate.Raw)
	if got := endPoint(certificate); string(got) != string(want[:]) {
		t.Fatalf("endPoint = %x, want %x", got, want)
	}
	certificate.SignatureAlgorithm = x509.SHA384WithRSA
	if got, want := endPoint(certificate), sha512.Sum384(certificate.Raw); string(got) != string(want[:]) {
		t.Fatalf("endPoint of a SHA-384 certificate = %x, want %x", got, want)
	}
	certificate.SignatureAlgorithm = x509.PureEd25519
	if endPoint(certificate) != nil {
		t.Fatal("an Ed25519 certificate has no tls-server-end-point hash")
	}
}

// A direct TLS connection starts TLS at once, asking for ALPN postgresql, and
// refuses a server that does not answer with it.
func TestDirectTLS(t *testing.T) {
	_, _, pair := selfSigned(t, t.TempDir(), "")
	for _, protocols := range [][]string{{"postgresql"}, nil} {
		listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}, NextProtos: protocols})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			// The startup packet arrives over TLS: answer it with an error.
			var length [4]byte
			if _, err := conn.Read(length[:]); err != nil {
				return
			}
			body := AppendString(AppendString([]byte{'S'}, "FATAL"), "")
			body = AppendString(append(body, 'C'), "28000")
			body = AppendString(append(body, 'M'), "direct TLS reached")
			message, mark := StartMessage(nil, MsgErrorResponse)
			_, _ = conn.Write(FinishMessage(append(append(message, body...), 0), mark))
		}()
		host, port, _ := net.SplitHostPort(listener.Addr().String())
		config, err := ParseConfig("host=" + host + " port=" + port + " user=u sslmode=require sslnegotiation=direct connect_timeout=5")
		if err != nil {
			t.Fatal(err)
		}
		_, err = Connect(context.Background(), config)
		var server *Error
		switch {
		case protocols != nil && (!errors.As(err, &server) || server.Message != "direct TLS reached"):
			t.Errorf("with ALPN: %v, want the server's error over TLS", err)
		case protocols == nil && !errors.Is(err, ErrTLSRefused):
			t.Errorf("without ALPN: %v, want ErrTLSRefused", err)
		}
		_ = listener.Close()
	}
}

// The mechanism and its header: PLUS where it can be had, y,, where the
// client could bind and the server offered no PLUS, which tells a server whose
// offer was struck out, and channel_binding=require refuses plain SCRAM
// before the password's proof is sent. It used to find out only once the
// server declared the login done.
func TestSCRAMMechanisms(t *testing.T) {
	both := []byte("SCRAM-SHA-256-PLUS\x00SCRAM-SHA-256\x00\x00")
	plain := []byte("SCRAM-SHA-256\x00\x00")
	binding := []byte("end point")
	tests := []struct {
		offered, binding []byte
		mode, mechanism  string
		header           string
		err              error
	}{
		{both, binding, "", "SCRAM-SHA-256-PLUS", "p=tls-server-end-point,,", nil},
		{both, binding, "disable", "SCRAM-SHA-256", "n,,", nil},
		{plain, binding, "prefer", "SCRAM-SHA-256", "y,,", nil},
		{plain, nil, "", "SCRAM-SHA-256", "n,,", nil},
		{both, nil, "require", "", "", ErrChannelBinding},
		{plain, binding, "require", "", "", ErrChannelBinding},
		{[]byte("SCRAM-SHA-1\x00\x00"), nil, "", "", "", ErrAuthMethod},
	}
	for _, test := range tests {
		mechanism, header, err := scramMechanisms(test.offered, test.binding, test.mode)
		if mechanism != test.mechanism || header != test.header || !errors.Is(err, test.err) {
			t.Errorf("%q, binding %v, %q: %s %s %v", test.offered, test.binding != nil, test.mode, mechanism, header, err)
		}
	}
}

func connectWith(t *testing.T, variable, extra string) error {
	t.Helper()
	dsn := os.Getenv(variable)
	if dsn == "" {
		t.Skip(variable + " not set")
	}
	config, err := ParseConfig(dsn + extra)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := Connect(t.Context(), config)
	if err == nil {
		_ = conn.Close(t.Context())
	}
	return err
}

// channel_binding=require logs in with SCRAM-SHA-256-PLUS over TLS, and
// refuses anything else before the password is sent.
func TestLiveChannelBinding(t *testing.T) {
	if err := connectWith(t, "DATABASE_URL_SCRAM", "&channel_binding=require"); err != nil {
		t.Errorf("SCRAM over TLS with channel binding required: %v", err)
	}
	if err := connectWith(t, "DATABASE_URL_SCRAM", "&channel_binding=disable"); err != nil {
		t.Errorf("SCRAM with channel binding disabled: %v", err)
	}
	if err := connectWith(t, "DATABASE_URL_SCRAM", "&channel_binding=require&sslmode=disable"); !errors.Is(err, ErrChannelBinding) {
		t.Errorf("channel binding required without TLS: %v, want ErrChannelBinding", err)
	}
	if err := connectWith(t, "DATABASE_URL_MD5", "&channel_binding=require"); !errors.Is(err, ErrChannelBinding) {
		t.Errorf("channel binding required of MD5: %v, want ErrChannelBinding", err)
	}
	if err := connectWith(t, "DATABASE_URL", "&channel_binding=require"); !errors.Is(err, ErrChannelBinding) {
		t.Errorf("channel binding required of a server that asks for nothing: %v, want ErrChannelBinding", err)
	}
}

// require_auth refuses a method it does not allow, and a server that asks for
// none where none is not allowed.
func TestLiveRequireAuth(t *testing.T) {
	tests := []struct {
		variable, auth string
		ok             bool
	}{
		{"DATABASE_URL_SCRAM", "scram-sha-256", true},
		{"DATABASE_URL_MD5", "scram-sha-256", false},
		{"DATABASE_URL_MD5", "md5,scram-sha-256", true},
		{"DATABASE_URL_PLAIN", "!password", false},
		{"DATABASE_URL_PLAIN", "password", true},
		{"DATABASE_URL", "none", true},
		{"DATABASE_URL", "scram-sha-256", false},
		{"DATABASE_URL", "!none", false},
	}
	// A login refused here is not retried in plaintext, as sslmode=prefer
	// retries a TLS failure: it would be refused the same.
	dsn := os.Getenv("DATABASE_URL_MD5")
	if dsn == "" {
		t.Skip("DATABASE_URL_MD5 not set")
	}
	config, err := ParseConfig(strings.Replace(dsn, "sslmode=require", "sslmode=prefer", 1) + "&require_auth=scram-sha-256")
	if err != nil {
		t.Fatal(err)
	}
	dials := 0
	config.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials++
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	if _, err := Connect(t.Context(), config); !errors.Is(err, ErrRequireAuth) || dials != 1 {
		t.Errorf("refused under sslmode=prefer: %v after %d dials, want ErrRequireAuth after 1", err, dials)
	}

	for _, test := range tests {
		err := connectWith(t, test.variable, "&require_auth="+test.auth)
		if test.ok != (err == nil) || (!test.ok && !errors.Is(err, ErrRequireAuth)) {
			t.Errorf("%s with require_auth=%s: %v", test.variable, test.auth, err)
		}
	}
}
