// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

// OAuthRequest is what Config.OAuthToken is asked for a token with: the
// connection's oauth_issuer, oauth_client_id, oauth_client_secret and
// oauth_scope, as libpq names them.
type OAuthRequest struct {
	// Issuer is the authorization server's issuer identifier, or its
	// discovery URL, as the server's validator is configured to trust.
	Issuer string
	// ClientID and ClientSecret identify the client to the issuer. The
	// secret is a credential: caduceus puts it in no error.
	ClientID     string
	ClientSecret string
	// Scope is the space-separated scopes to ask the issuer for.
	Scope string
}
