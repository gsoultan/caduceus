// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import "strings"

// defaultKerberosService is the service part of the server's principal when
// krbsrvname names none, as in libpq.
const defaultKerberosService = "postgres"

// beginGSS answers a server asking for Kerberos, as method: gss, or sspi,
// which a Kerberos client answers the same way, as libpq does outside
// Windows. The first token goes in a GSSResponse.
func (a *authenticator) beginGSS(method string) error {
	if err := a.method(method); err != nil {
		return err
	}
	if a.conn.config.NewGSS == nil {
		return ErrNoGSS
	}
	gss, err := a.conn.config.NewGSS()
	if err != nil {
		return err
	}
	a.gss = gss
	token, err := a.initToken()
	if err != nil {
		return err
	}
	return a.send(token)
}

// initToken asks for the first token, for KerberosSPN or for service/host.
func (a *authenticator) initToken() ([]byte, error) {
	config := a.conn.config
	if config.KerberosSPN != "" {
		return a.gss.GetInitTokenFromSPN(config.KerberosSPN)
	}
	service := config.KerberosServiceName
	if service == "" {
		service = defaultKerberosService
	}
	return a.gss.GetInitToken(kerberosHost(config.Host), service)
}

// kerberosHost is the host part of the server's principal. A Unix socket has
// no name of its own, and libpq names the local host for it.
func kerberosHost(host string) string {
	if host == "" || strings.HasPrefix(host, "/") || strings.HasPrefix(host, "@") {
		return "localhost"
	}
	return host
}

// continueGSS passes a token from the server on, and sends the reply, if
// there is one.
func (a *authenticator) continueGSS(token []byte) error {
	if a.gss == nil {
		return ErrProtocol
	}
	_, reply, err := a.gss.Continue(token)
	if err != nil || len(reply) == 0 {
		return err
	}
	return a.send(reply)
}
