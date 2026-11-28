// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"errors"
)

// attempt is one Connect: the servers tried so far, why each failed, and the
// connection chosen, or held as a last resort.
type attempt struct {
	config *Config
	conn   *Conn
	// held is a server ValidateConnect found valid but not preferred, used if
	// no server after it is preferred.
	held *Conn
	errs []error
}

// server tries one server, at each address LookupFunc gives for it, and
// reports whether to stop: a connection was chosen, or it failed in a way no
// other server could do better than.
func (a *attempt) server(ctx context.Context, server FallbackConfig) bool {
	addresses, err := a.addresses(ctx, server)
	if err != nil {
		a.fail(a.config.at(server), err)
		return ctx.Err() != nil
	}
	for _, address := range addresses {
		server.Host = address
		if a.address(ctx, a.config.at(server)) {
			return true
		}
	}
	return false
}

// addresses is what LookupFunc makes of the server's host: the host itself
// without one, for a socket directory, and where hostaddr gives the address.
//
// With LoadBalanceHosts they are shuffled, and a host is looked up here even
// without LookupFunc, so that its addresses are shuffled too.
func (a *attempt) addresses(ctx context.Context, server FallbackConfig) ([]string, error) {
	lookup := a.config.lookup()
	if lookup == nil || server.HostAddr != "" || (&Config{Host: server.Host}).unix() {
		return []string{server.Host}, nil
	}
	addresses, err := lookup(ctx, server.Host)
	return ordered(a.config, addresses), err
}

// address connects to the server one names and asks ValidateConnect of it,
// and reports whether to stop.
func (a *attempt) address(ctx context.Context, one *Config) bool {
	conn, err := connectOnce(ctx, one)
	if err != nil {
		a.fail(one, err)
		return final(ctx, err)
	}
	if a.config.ValidateConnect == nil {
		a.conn = conn
		return true
	}
	err = a.config.ValidateConnect(ctx, conn)
	switch {
	case err == nil:
		a.conn = conn
		return true
	case errors.Is(err, ErrNotPreferred) && a.held == nil:
		a.held = conn
		return false
	}
	_ = conn.Close(context.WithoutCancel(ctx))
	if !errors.Is(err, ErrNotPreferred) {
		a.fail(one, err)
	}
	return ctx.Err() != nil
}

func (a *attempt) fail(one *Config, err error) {
	a.errs = append(a.errs, connectError(one, err))
}

// result is the connection chosen, or the one held, set up by AfterConnect;
// or why every server failed.
func (a *attempt) result(ctx context.Context) (*Conn, error) {
	conn := a.conn
	switch {
	case conn == nil:
		conn = a.held
	case a.held != nil:
		_ = a.held.Close(context.WithoutCancel(ctx))
	}
	if conn == nil {
		return nil, a.failure()
	}
	if a.config.AfterConnect == nil {
		return conn, nil
	}
	if err := a.config.AfterConnect(ctx, conn); err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, connectError(conn.config, err)
	}
	return conn, nil
}

// failure is every server's failure: the one, or all of them joined, the
// first first.
func (a *attempt) failure() error {
	if len(a.errs) == 1 {
		return a.errs[0]
	}
	return errors.Join(a.errs...)
}

// final reports whether a failure to connect ends the attempt: the context
// is over, or the server refused what any other would refuse too.
func final(ctx context.Context, err error) bool {
	var server *Error
	if ctx.Err() != nil || !errors.As(err, &server) {
		return ctx.Err() != nil
	}
	switch server.Code {
	case "28P01", "3D000", "42501":
		return true
	}
	return false
}
