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
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Every libpq connection keyword is read here and none is sent to the server,
// which would refuse the connection over a setting it does not know.
func TestParseLibpqConnectionKeywords(t *testing.T) {
	config, err := ParseConfig("host=a user=u keepalives_idle=30 keepalives_interval=5 keepalives_count=3 " +
		"tcp_user_timeout=1500 load_balance_hosts=random requirepeer=postgres fallback_application_name=fb " +
		"sslmode=require sslcertmode=disable ssl_min_protocol_version=TLSv1.3 sslcompression=0 sslcrl=/none sslcrldir=/none")
	if err != nil {
		t.Fatal(err)
	}
	if want := (net.KeepAliveConfig{Idle: 30 * time.Second, Interval: 5 * time.Second, Count: 3}); config.KeepAlive != want {
		t.Errorf("KeepAlive = %+v, want %+v", config.KeepAlive, want)
	}
	if config.TCPUserTimeout != 1500*time.Millisecond || !config.LoadBalanceHosts || config.RequirePeer != "postgres" || config.SSLCertMode != "disable" {
		t.Errorf("config = %+v", config)
	}
	if config.TLSConfig.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = %x, want TLS 1.3", config.TLSConfig.MinVersion)
	}
	if want := map[string]string{"application_name": "fb"}; len(config.RuntimeParams) != 1 || config.RuntimeParams["application_name"] != "fb" {
		t.Errorf("RuntimeParams = %v, want %v", config.RuntimeParams, want)
	}

	named, err := ParseConfig("host=a user=u application_name=mine fallback_application_name=fb keepalives=0 requiressl=1")
	if err != nil {
		t.Fatal(err)
	}
	if named.RuntimeParams["application_name"] != "mine" || !named.NoKeepAlive || named.TLSConfig == nil || named.TLSFallback {
		t.Errorf("application_name %q, NoKeepAlive %v, TLS %v fallback %v", named.RuntimeParams["application_name"], named.NoKeepAlive, named.TLSConfig != nil, named.TLSFallback)
	}

	for _, bad := range []string{
		"keepalives=2", "keepalives_idle=x", "keepalives_count=-1", "tcp_user_timeout=soon", "load_balance_hosts=yes",
		"sslcertmode=maybe", "sslmode=require ssl_min_protocol_version=SSLv3",
		"sslmode=require ssl_min_protocol_version=TLSv1.3 ssl_max_protocol_version=TLSv1.2",
		"scram_client_key=x",
	} {
		if _, err := ParseConfig("host=a user=u " + bad); !errors.Is(err, ErrInvalidConnString) {
			t.Errorf("%s: err = %v, want ErrInvalidConnString", bad, err)
		}
	}
}

// The dialer keeps the five-minute keepalive caduceus always had unless told
// otherwise, and turns keepalives off for keepalives=0.
func TestNetDialer(t *testing.T) {
	if d := (&Config{}).netDialer(); !d.KeepAliveConfig.Enable || d.KeepAliveConfig.Idle != 5*time.Minute || d.KeepAliveConfig.Interval != 5*time.Minute {
		t.Errorf("default keepalive = %+v", d.KeepAliveConfig)
	}
	if d := (&Config{KeepAlive: net.KeepAliveConfig{Idle: time.Second, Count: 2}}).netDialer(); d.KeepAliveConfig.Idle != time.Second || d.KeepAliveConfig.Count != 2 {
		t.Errorf("keepalive = %+v", d.KeepAliveConfig)
	}
	if d := (&Config{NoKeepAlive: true}).netDialer(); d.KeepAliveConfig.Enable || d.KeepAlive >= 0 {
		t.Errorf("keepalives=0 left them on: %+v", d)
	}
	if d := (&Config{TCPUserTimeout: time.Second}).netDialer(); runtime.GOOS == "linux" && d.Control == nil {
		t.Error("tcp_user_timeout set nothing on the socket")
	}
}

// pki is a certificate authority, a server certificate it issued, and a
// revocation list of its own that names that certificate.
type pki struct {
	root, crl string
	server    tls.Certificate
}

func newPKI(t *testing.T) pki {
	t.Helper()
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "caduceus test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ = x509.ParseCertificate(caDER)
	serverKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	crlDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(1), ThisUpdate: time.Now().Add(-time.Hour), NextUpdate: time.Now().Add(time.Hour),
		RevokedCertificateEntries: []x509.RevocationListEntry{{SerialNumber: big.NewInt(42), RevocationTime: time.Now()}},
	}, ca, caKey)
	if err != nil {
		t.Fatal(err)
	}
	p := pki{root: filepath.Join(dir, "root.crt"), crl: filepath.Join(dir, "root.crl")}
	if err := os.WriteFile(p.root, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.crl, pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crlDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	p.server = tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: serverKey}
	return p
}

// refusingTLSServer accepts one TLS connection per test, after answering an
// SSLRequest with S, and refuses the login it then reads: reaching that
// refusal is how a test knows the handshake was accepted.
func refusingTLSServer(t *testing.T, config *tls.Config) (string, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var request [8]byte
				if _, err := conn.Read(request[:]); err != nil {
					return
				}
				_, _ = conn.Write([]byte{'S'})
				secured := tls.Server(conn, config)
				var length [4]byte
				if _, err := secured.Read(length[:]); err != nil {
					return
				}
				body := AppendString(AppendString([]byte{'S'}, "FATAL"), "")
				body = AppendString(append(body, 'C'), "28000")
				body = AppendString(append(body, 'M'), "handshake accepted")
				message, mark := StartMessage(nil, MsgErrorResponse)
				_, _ = secured.Write(FinishMessage(append(append(message, body...), 0), mark))
			}()
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	return host, port
}

func accepted(err error) bool {
	var server *Error
	return errors.As(err, &server) && server.Message == "handshake accepted"
}

// sslcrl refuses a server whose certificate its CA has revoked, and only
// a connection that verifies the certificate consults it, as in libpq.
func TestRevocationList(t *testing.T) {
	p := newPKI(t)
	host, port := refusingTLSServer(t, &tls.Config{Certificates: []tls.Certificate{p.server}})
	base := "host=" + host + " port=" + port + " user=u connect_timeout=5 sslsni=0 sslrootcert=" + p.root
	for _, tc := range []struct {
		settings string
		revoked  bool
	}{
		{" sslmode=verify-ca", false},
		{" sslmode=verify-ca sslcrl=" + p.crl, true},
		{" sslmode=verify-ca sslcrldir=" + filepath.Dir(p.crl), true},
		{" sslmode=require sslrootcert='' sslcrl=" + p.crl, false},
	} {
		config, err := ParseConfig(base + tc.settings)
		if err != nil {
			t.Fatalf("%s: %v", tc.settings, err)
		}
		_, err = Connect(context.Background(), config)
		switch {
		case tc.revoked && !errors.Is(err, ErrRevoked):
			t.Errorf("%s: err = %v, want ErrRevoked", tc.settings, err)
		case !tc.revoked && !accepted(err):
			t.Errorf("%s: err = %v, want the handshake accepted", tc.settings, err)
		}
	}
	if _, err := ParseConfig(base + " sslmode=verify-ca sslcrl=" + p.root); !errors.Is(err, ErrInvalidConnString) {
		t.Errorf("a file with no list in it: err = %v", err)
	}
}

// sslcertmode=require fails unless the server asks for a certificate.
func TestSSLCertModeRequire(t *testing.T) {
	p := newPKI(t)
	for _, tc := range []struct {
		auth tls.ClientAuthType
		ok   bool
	}{{tls.NoClientCert, false}, {tls.RequestClientCert, true}} {
		host, port := refusingTLSServer(t, &tls.Config{Certificates: []tls.Certificate{p.server}, ClientAuth: tc.auth})
		config, err := ParseConfig("host=" + host + " port=" + port + " user=u connect_timeout=5 sslmode=require sslcertmode=require")
		if err != nil {
			t.Fatal(err)
		}
		_, err = Connect(context.Background(), config)
		switch {
		case tc.ok && !accepted(err):
			t.Errorf("server asks: err = %v, want the handshake accepted", err)
		case !tc.ok && !errors.Is(err, ErrSSLCertMode):
			t.Errorf("server does not ask: err = %v, want ErrSSLCertMode", err)
		}
	}
}
