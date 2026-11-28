// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

// oauthRefusal is the JSON a server answers a token it does not accept
// with (RFC 7628, section 3.2.2).
type oauthRefusal struct {
	Status    string `json:"status"`
	Scope     string `json:"scope"`
	Discovery string `json:"openid-configuration"`
}
