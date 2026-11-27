// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

// Authentication request codes, the first four bytes of an 'R' message.
const (
	authOK           = 0
	authCleartext    = 3
	authMD5          = 5
	authSASL         = 10
	authSASLContinue = 11
	authSASLFinal    = 12
)

const (
	scramMechanism     = "SCRAM-SHA-256"
	scramPlusMechanism = "SCRAM-SHA-256-PLUS"
)

// maxLoginMessages bounds the exchange before ReadyForQuery, so a server that
// never finishes logging in cannot keep a dial going for ever on a context
// with no deadline.
const maxLoginMessages = 1024

// alpnProtocol is the ALPN protocol a direct TLS connection asks for.
const alpnProtocol = "postgresql"

// Connect dials the server described by config and logs in: Host and Port,
// or, failing that, each of Fallbacks in turn, until one will have it and
// ValidateConnect, if set, accepts it. AfterConnect then runs on it. A
// failure is a *ConnectError for each server tried, joined.
//
// With sslmode=prefer, a server that declines TLS or fails the handshake is
// dialled again in plaintext, as libpq does.
func Connect(ctx context.Context, config *Config) (*Conn, error) {
	if err := config.validAuth(); err != nil {
		return nil, err
	}
	a := attempt{config: config}
	for _, server := range config.servers() {
		if a.server(ctx, server) {
			break
		}
	}
	return a.result(ctx)
}

// connectOnce connects to the one server one names, within ConnectTimeout.
func connectOnce(ctx context.Context, one *Config) (*Conn, error) {
	if one.ConnectTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, one.ConnectTimeout)
		defer cancel()
	}
	conn, err := dial(ctx, one, one.firstProtocol())
	if retry, ok := retryVersion(one, err); ok {
		return dial(ctx, one, retry)
	}
	return conn, err
}

// retryVersion is the version to ask for once more when a startup packet's
// version was refused outright: 3.0, if min_protocol_version allows it.
// What 3.0 is answered with is the result, so there is one more dial at most.
func retryVersion(config *Config, err error) (ProtocolVersion, bool) {
	var refusal *versionRefusal
	if !errors.As(err, &refusal) || config.minProtocol() > Protocol30 {
		return 0, false
	}
	return Protocol30, true
}

// dial connects asking for a protocol version, in plaintext once more if
// TLS is preferred but not to be had.
func dial(ctx context.Context, config *Config, version ProtocolVersion) (*Conn, error) {
	conn, err := connect(ctx, config, config.TLSConfig, version)
	if err == nil || config.TLSConfig == nil || !config.TLSFallback || !fallbackWorthy(err) {
		return conn, err
	}
	return connect(ctx, config, nil, version)
}

// fallbackWorthy reports whether a failure was TLS's rather than the server's
// verdict on the login, which is the only kind a plaintext retry could cure.
//
// A login refused here, for require_auth or channel_binding, is not one:
// plaintext would be refused the same, or worse, let through unbound.
func fallbackWorthy(err error) bool {
	var serverErr *Error
	refused := errors.Is(err, ErrRequireAuth) || errors.Is(err, ErrChannelBinding)
	return !errors.As(err, &serverErr) && ctxErr(err) == nil && !refused
}

func ctxErr(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func connect(ctx context.Context, config *Config, tlsConfig *tls.Config, version ProtocolVersion) (*Conn, error) {
	network, address := config.target()
	netConn, err := config.dialer()(ctx, network, address)
	if err != nil {
		return nil, err
	}

	// A context ending mid-login has to interrupt a read already blocked, and a
	// deadline in the past is how a connection is told to.
	stop := context.AfterFunc(ctx, func() { _ = netConn.SetDeadline(time.Unix(1, 0)) })
	if deadline, ok := ctx.Deadline(); ok {
		_ = netConn.SetDeadline(deadline)
	}

	c := &Conn{
		netConn:  netConn,
		config:   config,
		rbuf:     make([]byte, initialReadBuffer),
		params:   make(map[string]string, 16),
		protocol: version,
	}
	err = c.login(ctx, tlsConfig)
	interrupted := !stop()
	if err == nil && interrupted {
		// Logged in, but the deadline was already moved into the past.
		err = context.Cause(ctx)
	}
	if err != nil {
		_ = c.netConn.Close()
		if isTimeout(err) {
			awaitExpiry(ctx)
		}
		if ctx.Err() != nil && ctxErr(err) == nil {
			err = errors.Join(context.Cause(ctx), err)
		}
		return nil, err
	}
	_ = c.netConn.SetDeadline(time.Time{})
	return c, nil
}

func (c *Conn) login(ctx context.Context, tlsConfig *tls.Config) error {
	if err := c.secure(ctx, tlsConfig); err != nil {
		return err
	}
	if _, err := c.netConn.Write(c.startupMessage()); err != nil {
		return err
	}

	policy, err := parseRequireAuth(c.config.RequireAuth)
	if err != nil {
		return err
	}
	auth := authenticator{conn: c, policy: policy}
	asked := c.protocol
	for i := range maxLoginMessages {
		kind, body, err := c.Receive()
		if err != nil {
			return err
		}
		switch kind {
		case MsgAuthentication:
			if err := auth.handle(body); err != nil {
				return err
			}
		case MsgBackendKeyData:
			if !c.cancelKey(body) {
				return ErrProtocol
			}
		case MsgErrorResponse:
			return startupError(asked, i, parseError(body))
		case MsgReadyForQuery:
			return auth.finish()
		case MsgNegotiateProtocol:
			// The server speaks an older minor version than asked for, and
			// offers the newest it does.
			if err := c.negotiate(body); err != nil {
				return err
			}
		default:
			return ErrProtocol
		}
	}
	return ErrProtocol
}

// startupError is the server's error answering the startup packet. As the
// first message, before any authentication, an error of protocol 08P01 or
// 0A000 to a packet asking for more than 3.0 is most likely a refusal of the
// version, by something in front of the server that knows only 3.0, such as
// PgBouncer before 1.24: it is a *versionRefusal, on which connectOnce asks
// once more, for 3.0. A current PostgreSQL offers 3.0 rather than refusing.
func startupError(asked ProtocolVersion, at int, failure *Error) error {
	if at == 0 && refusesProtocol(asked, failure) {
		return &versionRefusal{asked: asked, failure: failure}
	}
	return failure
}

// negotiate takes up the version a NegotiateProtocolVersion offers: the
// newest minor version the server speaks, which must be older than the one
// asked for and no older than the oldest allowed. The names of options it did
// not recognise follow, and none are sent.
func (c *Conn) negotiate(body []byte) error {
	if len(body) < 8 {
		return ErrProtocol
	}
	offered := ProtocolVersion(3<<16 | be32(body)&0xffff)
	switch {
	case offered >= c.protocol:
		return ErrProtocol
	case offered < c.config.minProtocol():
		return fmt.Errorf("%w: the server speaks protocol %s, and min_protocol_version is %s",
			ErrProtocolVersion, offered, c.config.minProtocol())
	}
	c.protocol = offered
	return nil
}

// cancelKey takes up the process ID and cancel key of a BackendKeyData, and
// reports whether they are well formed: a key of 4 bytes in protocol 3.0, of
// 4 to 256 in 3.2.
func (c *Conn) cancelKey(body []byte) bool {
	key := len(body) - 4
	if key < 4 || key > maxCancelKey || (c.protocol == Protocol30 && key != 4) {
		return false
	}
	c.pid, c.secret = be32(body), append([]byte(nil), body[4:]...)
	return true
}

// secure starts TLS, if it is to be used: at once with DirectTLS, or once
// the server has agreed to it.
func (c *Conn) secure(ctx context.Context, tlsConfig *tls.Config) error {
	switch {
	case tlsConfig == nil:
		return nil
	case c.config.DirectTLS:
		return c.directTLS(ctx, tlsConfig)
	}
	return c.negotiateTLS(ctx, tlsConfig)
}

// directTLS starts TLS at once, with the ALPN protocol postgresql, by which
// the server knows it is not some other protocol's client, and refuses a
// server that does not answer with it.
func (c *Conn) directTLS(ctx context.Context, tlsConfig *tls.Config) error {
	direct := tlsConfig.Clone()
	direct.NextProtos = []string{alpnProtocol}
	secured := tls.Client(c.netConn, direct)
	if err := secured.HandshakeContext(ctx); err != nil {
		return err
	}
	c.netConn = secured
	if secured.ConnectionState().NegotiatedProtocol != alpnProtocol {
		return fmt.Errorf("%w: the server did not answer with ALPN %s", ErrTLSRefused, alpnProtocol)
	}
	return nil
}

// negotiateTLS asks for encryption before anything else is sent, so that no
// credential and no setting crosses in the clear when encryption is available.
func (c *Conn) negotiateTLS(ctx context.Context, config *tls.Config) error {
	request := binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, 8), sslRequestCode)
	if _, err := c.netConn.Write(request); err != nil {
		return err
	}
	// Exactly one byte, from the connection itself: anything buffered here could
	// belong to the TLS handshake.
	var answer [1]byte
	if _, err := io.ReadFull(c.netConn, answer[:]); err != nil {
		return err
	}
	switch answer[0] {
	case 'S':
		secured := tls.Client(c.netConn, config)
		if err := secured.HandshakeContext(ctx); err != nil {
			return err
		}
		c.netConn = secured
		return nil
	case 'N':
		return ErrTLSRefused
	}
	return ErrProtocol
}

func (c *Conn) startupMessage() []byte {
	body := binary.BigEndian.AppendUint32(make([]byte, 4, 256), uint32(c.protocol))
	body = AppendString(AppendString(body, "user"), c.config.User)
	body = AppendString(AppendString(body, "database"), c.config.Database)

	// In a fixed order, so two connections never differ by map iteration.
	names := make([]string, 0, len(c.config.RuntimeParams))
	for name := range c.config.RuntimeParams {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body = AppendString(AppendString(body, name), c.config.RuntimeParams[name])
	}
	body = append(body, 0)
	binary.BigEndian.PutUint32(body, uint32(len(body)))
	return body
}

// authenticator answers the server's authentication requests, within what
// require_auth and channel_binding allow.
type authenticator struct {
	conn     *Conn
	scram    *scramClient
	verified bool
	policy   authPolicy
	// asked is a method asked for, and bound a SCRAM bound to the TLS
	// connection.
	asked bool
	bound bool
}

// requireBinding reports whether channel_binding is require.
func (a *authenticator) requireBinding() bool {
	return a.conn.config.ChannelBinding == "require"
}

// password checks that the server may ask for the password as method, in
// the clear or hashed with MD5, before it is sent.
func (a *authenticator) password(method string) error {
	a.asked = true
	if err := a.policy.check(method); err != nil {
		return err
	}
	if a.requireBinding() {
		return ErrChannelBinding
	}
	return nil
}

// authenticated checks a login the server has declared done: one that
// asked for nothing must be allowed to, and a binding required was made.
func (a *authenticator) authenticated() error {
	if !a.asked {
		if err := a.policy.check("none"); err != nil {
			return err
		}
	}
	if a.requireBinding() && !a.bound {
		return ErrChannelBinding
	}
	return nil
}

func (a *authenticator) handle(body []byte) error {
	if len(body) < 4 {
		return ErrProtocol
	}
	code, data := be32(body), body[4:]
	switch code {
	case authOK:
		return a.authenticated()
	case authCleartext:
		if err := a.password("password"); err != nil {
			return err
		}
		return a.send(AppendString(nil, a.conn.config.Password))
	case authMD5:
		if len(data) != 4 {
			return ErrProtocol
		}
		if err := a.password("md5"); err != nil {
			return err
		}
		return a.send(AppendString(nil, md5Response(a.conn.config.User, a.conn.config.Password, data)))
	case authSASL:
		a.asked = true
		if err := a.policy.check("scram-sha-256"); err != nil {
			return err
		}
		return a.beginSCRAM(data)
	case authSASLContinue:
		return a.continueSCRAM(data)
	case authSASLFinal:
		return a.finishSCRAM(data)
	}
	return ErrAuthMethod
}

// finish checks the login ended in a state the connection can be used from.
// A server that began SCRAM and then declared success without proving it knew
// the verifier has not authenticated itself.
func (a *authenticator) finish() error {
	if a.scram != nil && !a.verified {
		return ErrSCRAM
	}
	return nil
}

func (a *authenticator) send(body []byte) error {
	message, mark := StartMessage(nil, 'p')
	_, err := a.conn.netConn.Write(FinishMessage(append(message, body...), mark))
	return err
}

// beginSCRAM begins SCRAM with the mechanism scramMechanisms picks.
func (a *authenticator) beginSCRAM(mechanisms []byte) error {
	binding := a.conn.serverEndPoint()
	mechanism, header, err := scramMechanisms(mechanisms, binding, a.conn.config.ChannelBinding)
	if err != nil {
		return err
	}
	a.scram = &scramClient{password: a.conn.config.Password, header: header}
	if mechanism == scramPlusMechanism {
		a.scram.binding, a.bound = binding, true
	}
	first, err := a.scram.first()
	if err != nil {
		return err
	}
	body := AppendString(nil, mechanism)
	body = AppendInt32(body, int32(len(first)))
	return a.send(append(body, first...))
}

func (a *authenticator) continueSCRAM(challenge []byte) error {
	if a.scram == nil {
		return ErrProtocol
	}
	final, err := a.scram.final(challenge)
	if err != nil {
		return err
	}
	return a.send(final)
}

func (a *authenticator) finishSCRAM(signature []byte) error {
	if a.scram == nil {
		return ErrProtocol
	}
	if err := a.scram.verify(signature); err != nil {
		return err
	}
	a.verified = true
	return nil
}

// scramMechanisms picks the mechanism and its GS2 header: SCRAM-SHA-256-PLUS,
// bound to the TLS connection, where there is TLS (binding is its
// tls-server-end-point), the server offers it and channel_binding allows it;
// SCRAM-SHA-256 otherwise, its header saying whether the client could have
// bound it, so that a server whose offer of PLUS was struck out on the way can
// tell. channel_binding=require refuses SCRAM-SHA-256 before anything is
// sent.
func scramMechanisms(offered, binding []byte, channelBinding string) (string, string, error) {
	usable := channelBinding != "disable" && binding != nil
	plus := usable && offersMechanism(offered, scramPlusMechanism)
	switch {
	case plus:
		return scramPlusMechanism, "p=tls-server-end-point,,", nil
	case channelBinding == "require":
		return "", "", ErrChannelBinding
	case !offersMechanism(offered, scramMechanism):
		return "", "", ErrAuthMethod
	case usable:
		return scramMechanism, "y,,", nil
	}
	return scramMechanism, "n,,", nil
}

// offersMechanism reports whether a NUL-separated list names a mechanism.
func offersMechanism(list []byte, want string) bool {
	for len(list) > 0 {
		name, rest, ok := cutString(list)
		if !ok || name == "" {
			return false
		}
		if name == want {
			return true
		}
		list = rest
	}
	return false
}

// md5Response is PostgreSQL's MD5 password exchange: the stored hash, hashed
// again with a salt the server chose for this connection.
func md5Response(user, password string, salt []byte) string {
	inner := md5.Sum([]byte(password + user))
	outer := md5.Sum(append([]byte(hex.EncodeToString(inner[:])), salt...))
	return "md5" + hex.EncodeToString(outer[:])
}
