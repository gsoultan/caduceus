// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import "errors"

var (
	// ErrClosed is returned by any operation on a connection that has been
	// closed, by the caller or by a failure that left it unusable.
	ErrClosed = errors.New("caduceus/wire: connection is closed")

	// ErrProtocol is returned when the server sends something the protocol does
	// not allow at that point. The connection is closed: once the two sides
	// disagree about where a message starts, nothing after it can be trusted.
	ErrProtocol = errors.New("caduceus/wire: protocol violation")

	// ErrProtocolVersion is returned by Connect when the server speaks no
	// protocol version that min_protocol_version allows.
	ErrProtocolVersion = errors.New("caduceus/wire: the server speaks no protocol version allowed")

	// ErrMessageTooLarge is returned when the server announces a message larger
	// than Config.MaxMessageSize. The limit exists so that whoever answers on
	// the server's address cannot make the client allocate without bound.
	ErrMessageTooLarge = errors.New("caduceus/wire: message exceeds MaxMessageSize")

	// ErrTLSRefused is returned when TLS is required and the server declined it.
	ErrTLSRefused = errors.New("caduceus/wire: server refused TLS")

	// ErrAuthMethod is returned when the server asks for an authentication method
	// this package does not implement, such as GSSAPI or SCRAM with channel
	// binding only.
	ErrAuthMethod = errors.New("caduceus/wire: unsupported authentication method")
	// ErrNoGSS is a server that asked for Kerberos, gss or sspi, from a
	// connection whose Config.NewGSS is nil.
	ErrNoGSS = errors.New("caduceus/wire: the server asked for Kerberos (GSSAPI) and Config.NewGSS is nil: set it, to gss.New from github.com/gsoultan/caduceus/gss for one")

	// ErrOAuth is a login with OAuth (OAUTHBEARER, PostgreSQL 18) that
	// failed on the client's side, or whose token the server refused.
	ErrOAuth = errors.New("caduceus/wire: OAuth login failed")

	// ErrSCRAM is returned when the server does not hold up its half of
	// SCRAM-SHA-256. It says no more than that: the detail would be about a secret.
	ErrSCRAM = errors.New("caduceus/wire: server failed SCRAM authentication")

	// ErrNotPreferred is what a ValidateConnect returns of a server that will
	// do, but not as well as one of another kind, as a primary will do for
	// target_session_attrs=prefer-standby: it is used if no server after it
	// is preferred.
	ErrNotPreferred = errors.New("caduceus/wire: the server is not of the kind preferred")

	// ErrSessionAttrs is what ValidateConnect returns of a server that is not
	// of the kind target_session_attrs asks for.
	ErrSessionAttrs = errors.New("caduceus/wire: the server is not of the kind target_session_attrs asks for")

	// ErrRequireAuth is returned by Connect when the server asks for an
	// authentication method that Config.RequireAuth does not allow. Nothing
	// of the password has been sent.
	ErrRequireAuth = errors.New("caduceus/wire: the server's authentication method is not allowed by require_auth")

	// ErrChannelBinding is returned by Connect when Config.ChannelBinding is
	// require and the login would not be bound to the TLS connection: no TLS,
	// a server that does not offer SCRAM-SHA-256-PLUS, or a method other than
	// SCRAM. Nothing of the password has been sent.
	ErrChannelBinding = errors.New("caduceus/wire: channel binding is required and not to be had")

	// ErrRequirePeer is returned when requirepeer names a user and the server
	// at the other end of the Unix socket runs as another, or the system
	// cannot say who it runs as.
	ErrRequirePeer = errors.New("caduceus/wire: the server's process does not run as the user requirepeer names")

	// ErrSSLCertMode is returned when sslcertmode=require and the server did
	// not ask for a client certificate.
	ErrSSLCertMode = errors.New("caduceus/wire: sslcertmode=require and the server did not ask for a client certificate")

	// ErrRevoked is returned when a certificate the server presents is on a
	// revocation list sslcrl or sslcrldir names.
	ErrRevoked = errors.New("caduceus/wire: the server's certificate has been revoked")

	// ErrInvalidConnString is returned by ParseConfig. It never quotes the
	// string, because the string is where the password is.
	ErrInvalidConnString = errors.New("caduceus/wire: invalid connection string")
)
