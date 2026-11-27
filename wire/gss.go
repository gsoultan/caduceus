// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

// GSS is a GSSAPI client, for a server that authenticates with Kerberos:
// pg_hba.conf's gss, and sspi, which a Kerberos client answers the same way.
//
// caduceus speaks the protocol and leaves Kerberos to an implementation, so
// that it requires nothing: the module github.com/gsoultan/caduceus/gss
// provides one, on a pure-Go Kerberos library, and Config.NewGSS takes it or
// any other. The interface is pgx's, so a provider written for pgx fits.
type GSS interface {
	// GetInitToken returns the first token for the service on host, whose
	// principal is service/host.
	GetInitToken(host, service string) ([]byte, error)
	// GetInitTokenFromSPN returns the first token for a principal named in
	// full.
	GetInitTokenFromSPN(spn string) ([]byte, error)
	// Continue takes a token from the server and returns whether the
	// context is established, and a token to send back, if any.
	Continue(inToken []byte) (done bool, outToken []byte, err error)
}
