// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"crypto/tls"
	"fmt"
	"maps"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"time"
)

// DefaultMaxMessageSize is the largest message a connection accepts unless told
// otherwise. PostgreSQL's own ceiling on a single field is a gigabyte, so a
// limit below that would refuse rows the server is happy to send.
const DefaultMaxMessageSize = 1<<30 + 1<<20

// Config is everything needed to open a connection, resolved once from a
// connection string by ParseConfig. Opening many connections from one Config
// re-reads nothing: no environment, no password file, no certificate.
type Config struct {
	// Host is a hostname, an IP address, or the directory of a Unix socket
	// (any value starting with a slash).
	Host string
	Port uint16
	// HostAddr is libpq's hostaddr: the numeric address to dial instead of
	// looking Host up. Host is still the name TLS checks the certificate
	// against and the password file is searched by.
	HostAddr string

	User     string
	Password string
	Database string

	// RuntimeParams are sent in the startup packet: application_name,
	// search_path, options, replication and anything else the server accepts
	// there.
	RuntimeParams map[string]string

	// TLSConfig is nil when the connection is not to be encrypted.
	TLSConfig *tls.Config
	// TLSFallback allows a plaintext connection when the server declines TLS or
	// the handshake fails: libpq's sslmode=prefer, the default.
	TLSFallback bool

	// SSLKeyLogFile is libpq's sslkeylogfile: a file each TLS connection
	// appends its session keys to, in the NSS key log format Wireshark reads,
	// for debugging. It is a secret: with it, anyone holding a capture of the
	// traffic can read it. The file is created with mode 0600.
	SSLKeyLogFile string

	// DirectTLS starts TLS as soon as the connection is open, with the ALPN
	// protocol postgresql, rather than asking for it first: libpq's
	// sslnegotiation=direct, which saves a round trip and needs PostgreSQL 17
	// or later. It applies only where TLSConfig is set, and a server that
	// does not answer with ALPN postgresql is refused.
	DirectTLS bool

	// RequireAuth is libpq's require_auth: the authentication methods the
	// server may ask for, comma-separated, from password, md5,
	// scram-sha-256, gss, sspi and none (asking for nothing), or, each
	// prefixed with !, those it may not. Empty allows any. A method not
	// allowed fails with ErrRequireAuth before the password is sent.
	RequireAuth string

	// ChannelBinding is libpq's channel_binding: disable, prefer (the
	// default, for empty) or require. Over TLS, SCRAM is bound to the
	// connection as SCRAM-SHA-256-PLUS with tls-server-end-point when the
	// server offers it, which proves the server that holds the password's
	// verifier is the one TLS is with; require refuses any login that is
	// not, with ErrChannelBinding.
	ChannelBinding string

	// NewGSS makes the GSSAPI client a login with Kerberos uses, once for
	// each such login. Nil refuses one with ErrNoGSS. gss.New, from the
	// module github.com/gsoultan/caduceus/gss, is one, and caduceus requires
	// nothing so that only a program that sets it pulls a Kerberos library in.
	NewGSS func() (GSS, error)
	// KerberosServiceName is libpq's krbsrvname, the service part of the
	// server's principal: postgres, for empty, as in libpq. KerberosSPN,
	// pgx's krbspn, names the principal in full instead, for a server whose
	// principal is not service/host.
	KerberosServiceName string
	KerberosSPN         string
	// GSSEncMode is libpq's gssencmode: disable (or empty) does not
	// encrypt with GSSAPI; prefer encrypts with it, in place of TLS, where
	// NewGSS gives a provider that can (a GSSWrapper) and has credentials,
	// and the server agrees; require refuses a connection that is not
	// encrypted so, with ErrGSSEncRefused. libpq prefers by default where
	// it finds credentials; caduceus asks to be told. A Unix socket is
	// never encrypted.
	GSSEncMode string

	// OAuthToken gets the bearer token a server that asks for OAuth
	// (OAUTHBEARER, PostgreSQL 18) is logged in with, asked with the
	// connection's OAuth settings. Nil refuses such a login with ErrOAuth.
	// Getting a token, by a device flow or a client credentials grant, and
	// caching it, is the program's: libpq does it with libcurl and a
	// terminal, neither of which a library has.
	OAuthToken func(ctx context.Context, request OAuthRequest) (string, error)
	// OAuthIssuer, OAuthClientID, OAuthClientSecret and OAuthScope are
	// libpq's oauth_issuer, oauth_client_id, oauth_client_secret and
	// oauth_scope, handed to OAuthToken.
	OAuthIssuer       string
	OAuthClientID     string
	OAuthClientSecret string
	OAuthScope        string

	// KeepAlive sets TCP keepalives on a connection DialFunc does not open:
	// libpq's keepalives_idle, keepalives_interval and keepalives_count. A
	// zero Idle or Interval is five minutes, as before these were read, and a
	// zero Count is the system's. NoKeepAlive, libpq's keepalives=0, turns
	// them off. TCPUserTimeout is libpq's tcp_user_timeout, how long sent
	// data may go unacknowledged before the connection is dropped; it is
	// honoured on Linux, the only system with TCP_USER_TIMEOUT, and zero
	// leaves the system's.
	KeepAlive      net.KeepAliveConfig
	NoKeepAlive    bool
	TCPUserTimeout time.Duration

	// LoadBalanceHosts is libpq's load_balance_hosts=random: the servers,
	// Host and Fallbacks alike, and the addresses each resolves to, are tried
	// in a random order on each Connect, which spreads connections across
	// them.
	LoadBalanceHosts bool

	// RequirePeer is libpq's requirepeer: over a Unix socket, the name of the
	// operating-system user the server must run as, checked with the
	// socket's peer credentials before anything is sent. Empty checks
	// nothing. It is supported on Linux; elsewhere a Unix-socket connection
	// that sets it fails with ErrRequirePeer.
	RequirePeer string

	// SSLCertMode is libpq's sslcertmode: allow (the default, for empty)
	// sends the client certificate if the server asks for one, disable never
	// sends one, and require fails the connection with ErrSSLCertMode when
	// the server does not ask.
	SSLCertMode string

	// ConnectTimeout bounds dialling and logging in when the caller's context
	// has no earlier deadline. Zero means only the context bounds it.
	ConnectTimeout time.Duration

	// CancelDelay, when positive, keeps a connection whose context ends in the
	// middle of an exchange. A CancelRequest is sent at once, and the
	// exchange goes on reading the server's answer, usually an error with
	// SQLSTATE 57014, for up to CancelDelay; the connection is closed only if
	// that runs out. It is for a session a person drives, such as a SQL
	// console, where cancelling a long statement must not end the session
	// and lose its transaction, temporary tables and settings. Zero, the
	// default, sends the CancelRequest and closes the connection at once, so a
	// caller past its deadline never waits on the server.
	//
	// The cancel can arrive after the statement it was meant for has
	// finished, as every CancelRequest can: the server then cancels whatever
	// the connection is running when it arrives, which is nothing between
	// exchanges, but can be the next statement if one is sent at once.
	CancelDelay time.Duration

	// MaxMessageSize bounds a single message from the server. Zero means
	// DefaultMaxMessageSize.
	MaxMessageSize int

	// NoStreamWatermark leaves the socket's receive low-water mark alone.
	// By default, on Linux over TCP, it is raised to 64 KiB once a result has
	// run past 4 MiB, which wakes the reader an eighth as often; the end of
	// such a result is then delivered about 2 ms late, or later where the
	// machine is too busy to run the timer that lowers the mark on time. The
	// kernel is asked whether it wakes a reader when the mark is lowered by
	// its version, so a system that emulates Linux and reports a Linux
	// version, such as gVisor or WSL 1, is taken at its word: set this there
	// if long results stall at their end.
	NoStreamWatermark bool

	// MinProtocolVersion and MaxProtocolVersion bound the protocol version a
	// connection uses, as min_protocol_version and max_protocol_version do.
	// Zero means Protocol30 and Protocol32: the connection asks for 3.2,
	// which PostgreSQL 18 and later use, and an older server answers that it
	// speaks 3.0, which the connection then uses. Something in front of the
	// server that knows only 3.0 and refuses 3.2 outright, such as PgBouncer
	// before 1.24, is dialled once more asking for 3.0; a MaxProtocolVersion
	// of Protocol30 saves that second dial. A MinProtocolVersion of
	// Protocol32 refuses any server that does not speak 3.2, and forbids the
	// second dial: such a refusal fails with ErrProtocolVersion.
	MinProtocolVersion ProtocolVersion
	MaxProtocolVersion ProtocolVersion

	// OnNotice is called with each notice the server sends: a WARNING, or
	// the NOTICE of a RAISE NOTICE, which has the fields of an error and is
	// not one. Nil drops them. OnNotification is called with each
	// notification of a channel the connection LISTENs to, which is then not
	// queued for WaitForNotification. Both run on the goroutine reading the
	// connection, in the middle of whatever it is reading: they must not use
	// the connection, and should return quickly.
	OnNotice       func(notice *Error)
	OnNotification func(notification Notification)

	// Fallbacks are the servers to try, in order, after Host and Port. Connect
	// moves on to the next when one cannot be reached or refuses the login,
	// except where the next could not do better: a wrong password (28P01), a
	// database that does not exist (3D000) or a role without the privilege
	// (42501), which end the attempt there, as a context that is over does.
	// ConnectTimeout bounds each server on its own, as libpq's does.
	Fallbacks []*FallbackConfig

	// DialFunc opens the network connection; nil dials with a net.Dialer
	// that keeps the connection alive every five minutes, or as KeepAlive and
	// TCPUserTimeout say. LookupFunc, if set,
	// resolves a host to the addresses to try, in order, each as a server of
	// its own; nil leaves that to the dialer, which tries every address of a
	// host and reports the first that answers.
	DialFunc   func(ctx context.Context, network, address string) (net.Conn, error)
	LookupFunc func(ctx context.Context, host string) ([]string, error)

	// ValidateConnect, if set, is asked of each server logged into whether it
	// is the one wanted; one it refuses is closed and the next is tried. What
	// target_session_attrs sets is one of these: ValidateReadWrite,
	// ValidateReadOnly, ValidatePrimary, ValidateStandby or
	// ValidatePreferStandby. An error that is ErrNotPreferred keeps the
	// server as a last resort: it is used if none after it is valid.
	//
	// AfterConnect, if set, runs on the connection once it is chosen, to set
	// it up; an error from it closes the connection and fails Connect.
	ValidateConnect func(ctx context.Context, conn *Conn) error
	AfterConnect    func(ctx context.Context, conn *Conn) error
}

// Copy returns a deep copy, safe to adjust for one connection without
// affecting the template.
func (c *Config) Copy() *Config {
	clone := *c
	clone.RuntimeParams = maps.Clone(c.RuntimeParams)
	if c.TLSConfig != nil {
		clone.TLSConfig = c.TLSConfig.Clone()
	}
	clone.Fallbacks = make([]*FallbackConfig, len(c.Fallbacks))
	for i, fallback := range c.Fallbacks {
		copied := *fallback
		if fallback.TLSConfig != nil {
			copied.TLSConfig = fallback.TLSConfig.Clone()
		}
		clone.Fallbacks[i] = &copied
	}
	if len(clone.Fallbacks) == 0 {
		clone.Fallbacks = nil
	}
	return &clone
}

// validAuth checks RequireAuth and ChannelBinding, which a Config built by
// hand may hold anything in.
func (c *Config) validAuth() error {
	if _, err := parseRequireAuth(c.RequireAuth); err != nil {
		return err
	}
	switch c.ChannelBinding {
	case "", "disable", "prefer", "require":
		return nil
	}
	return fmt.Errorf("%w: channel_binding %q is not one of disable, prefer, require", ErrInvalidConnString, c.ChannelBinding)
}

// servers is where to connect, in order: Host and Port, then each fallback,
// or all of them shuffled when LoadBalanceHosts is set.
func (c *Config) servers() []FallbackConfig {
	servers := make([]FallbackConfig, 0, 1+len(c.Fallbacks))
	servers = append(servers, FallbackConfig{Host: c.Host, Port: c.Port, HostAddr: c.HostAddr, TLSConfig: c.TLSConfig, TLSFallback: c.TLSFallback})
	for _, fallback := range c.Fallbacks {
		servers = append(servers, *fallback)
	}
	return ordered(c, servers)
}

// at is the configuration of a connection to one server: this one, with that
// server's address and TLS, and no fallbacks of its own, so that what the
// connection reports as its configuration, and where it sends a cancel
// request, is the server it is connected to.
func (c *Config) at(server FallbackConfig) *Config {
	one := *c
	one.Host, one.Port, one.HostAddr = server.Host, server.Port, server.HostAddr
	one.TLSConfig, one.TLSFallback = server.TLSConfig, server.TLSFallback
	one.Fallbacks = nil
	return &one
}

// dialer is DialFunc, or a net.Dialer's.
func (c *Config) dialer() func(ctx context.Context, network, address string) (net.Conn, error) {
	if c.DialFunc != nil {
		return c.DialFunc
	}
	return c.netDialer().DialContext
}

// netDialer is the net.Dialer a connection is opened with when DialFunc is
// nil: keepalives as KeepAlive and NoKeepAlive say, and TCPUserTimeout.
func (c *Config) netDialer() *net.Dialer {
	dialer := &net.Dialer{KeepAlive: -1}
	if !c.NoKeepAlive {
		keepAlive := c.KeepAlive
		keepAlive.Enable = true
		if keepAlive.Idle == 0 {
			keepAlive.Idle = 5 * time.Minute
		}
		if keepAlive.Interval == 0 {
			keepAlive.Interval = 5 * time.Minute
		}
		dialer.KeepAliveConfig = keepAlive
	}
	if c.TCPUserTimeout > 0 {
		dialer.Control = tcpUserTimeout(c.TCPUserTimeout)
	}
	return dialer
}

// lookup is LookupFunc, or, to shuffle a host's addresses for
// LoadBalanceHosts, the system resolver; nil leaves resolving to the dialer.
func (c *Config) lookup() func(ctx context.Context, host string) ([]string, error) {
	if c.LookupFunc != nil || !c.LoadBalanceHosts {
		return c.LookupFunc
	}
	return net.DefaultResolver.LookupHost
}

// ordered is list in the order to try it: as given, or shuffled for
// LoadBalanceHosts.
func ordered[T any](c *Config, list []T) []T {
	if c.LoadBalanceHosts {
		rand.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
	}
	return list
}

// peer checks RequirePeer, which applies to a Unix socket only.
func (c *Config) peer(network string, conn net.Conn) error {
	if c.RequirePeer == "" || network != "unix" {
		return nil
	}
	return checkPeer(conn, c.RequirePeer)
}

// minProtocol is the oldest protocol version a connection may use.
func (c *Config) minProtocol() ProtocolVersion {
	if c.MinProtocolVersion == 0 {
		return Protocol30
	}
	return c.MinProtocolVersion
}

// firstProtocol is the version a connection asks for: the newest allowed.
func (c *Config) firstProtocol() ProtocolVersion {
	if c.MaxProtocolVersion == 0 {
		return Protocol32
	}
	return c.MaxProtocolVersion
}

// unix reports whether Host names a socket directory.
func (c *Config) unix() bool {
	return c.HostAddr == "" && strings.HasPrefix(c.Host, "/")
}

// target returns the network and address to dial.
func (c *Config) target() (string, string) {
	port := strconv.Itoa(int(c.Port))
	if c.HostAddr != "" {
		return "tcp", net.JoinHostPort(c.HostAddr, port)
	}
	if c.unix() {
		return "unix", c.Host + "/.s.PGSQL." + port
	}
	return "tcp", net.JoinHostPort(c.Host, port)
}

// noWatermark reports whether the low-water mark is to be left alone.
func (c *Config) noWatermark() bool {
	return c == nil || c.NoStreamWatermark
}

func (c *Config) maxMessage() int {
	if c.MaxMessageSize > 0 {
		return c.MaxMessageSize
	}
	return DefaultMaxMessageSize
}
