// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"fmt"
	"strings"
)

// authPolicy is require_auth: the authentication methods the server may ask
// for, or, negated, those it may not. The zero value allows any.
type authPolicy struct {
	methods map[string]bool
	negated bool
}

// authMethods are the names require_auth knows, as libpq names them.
var authMethods = map[string]bool{
	"password": true, "md5": true, "scram-sha-256": true, "gss": true, "sspi": true, "oauth": true, "none": true,
}

// parseRequireAuth reads require_auth: a comma-separated list of methods,
// every one of them prefixed with ! or none of them.
func parseRequireAuth(text string) (authPolicy, error) {
	if text == "" {
		return authPolicy{}, nil
	}
	policy := authPolicy{methods: map[string]bool{}, negated: strings.HasPrefix(text, "!")}
	for _, entry := range strings.Split(text, ",") {
		method, negated := strings.CutPrefix(entry, "!")
		switch {
		case negated != policy.negated:
			return authPolicy{}, fmt.Errorf("%w: require_auth mixes methods with and without !", ErrInvalidConnString)
		case !authMethods[method]:
			return authPolicy{}, fmt.Errorf("%w: require_auth method %q is not one of password, md5, scram-sha-256, gss, sspi, oauth, none", ErrInvalidConnString, method)
		case policy.methods[method]:
			return authPolicy{}, fmt.Errorf("%w: require_auth names %q twice", ErrInvalidConnString, method)
		}
		policy.methods[method] = true
	}
	return policy, nil
}

// allows reports whether the server may authenticate with method; "none" is
// a server that asks for nothing.
func (p authPolicy) allows(method string) bool {
	if p.methods == nil {
		return true
	}
	return p.methods[method] != p.negated
}

// check refuses method if the policy does not allow it.
func (p authPolicy) check(method string) error {
	if p.allows(method) {
		return nil
	}
	return fmt.Errorf("%w: the server asked for %s", ErrRequireAuth, method)
}
