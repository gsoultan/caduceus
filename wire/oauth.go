// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"encoding/json"
	"fmt"
	"regexp"
)

const oauthMechanism = "OAUTHBEARER"

// bearerToken is RFC 6750's b64token, all an Authorization header may carry.
var bearerToken = regexp.MustCompile(`^[A-Za-z0-9\-._~+/]+=*$`)

// beginOAuth answers a server that asks for OAUTHBEARER, PostgreSQL 18's
// OAuth login, with the token Config.OAuthToken gets. How it gets one, by a
// device flow, a client credentials grant or a token already held, is the
// program's: libpq's built-in device flow prints to a terminal, which a
// library has none of.
func (a *authenticator) beginOAuth() error {
	if err := a.method("oauth"); err != nil {
		return err
	}
	config := a.conn.config
	if config.OAuthToken == nil {
		return fmt.Errorf("%w: the server asked for OAuth and Config.OAuthToken is nil", ErrOAuth)
	}
	token, err := config.OAuthToken(a.ctx, OAuthRequest{
		Issuer: config.OAuthIssuer, ClientID: config.OAuthClientID,
		ClientSecret: config.OAuthClientSecret, Scope: config.OAuthScope,
	})
	if err != nil {
		return fmt.Errorf("%w: getting a token: %w", ErrOAuth, err)
	}
	if !bearerToken.MatchString(token) {
		return fmt.Errorf("%w: the token is not one a bearer header can carry", ErrOAuth)
	}
	a.oauth = true
	response := "n,,\x01auth=Bearer " + token + "\x01\x01"
	body := AppendString(nil, oauthMechanism)
	body = AppendInt32(body, int32(len(response)))
	return a.send(append(body, response...))
}

// refusedOAuth answers a server that refused the token: the client ends the
// exchange with a single separator, and the login fails with what the server
// said of the token.
func (a *authenticator) refusedOAuth(data []byte) error {
	var refusal oauthRefusal
	if err := json.Unmarshal(data, &refusal); err != nil {
		return fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	if err := a.send([]byte{1}); err != nil {
		return err
	}
	return fmt.Errorf("%w: the server refused the token: %s (scope %q, discovery %s)", ErrOAuth, refusal.Status, refusal.Scope, refusal.Discovery)
}

// offersSCRAM reports whether a SASL offer names a SCRAM mechanism.
func offersSCRAM(mechanisms []byte) bool {
	return offersMechanism(mechanisms, scramMechanism) || offersMechanism(mechanisms, scramPlusMechanism)
}
