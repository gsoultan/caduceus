// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import "crypto/tls"

// FallbackConfig is another server to try when the one a Config names cannot
// be connected to, or is not the kind ValidateConnect asks for: the second and
// later hosts of host=a,b,c. Everything but where to connect is the Config's.
type FallbackConfig struct {
	Host string
	Port uint16
	// HostAddr is this host's entry in hostaddr: the address to dial for it.
	HostAddr string

	// TLSConfig and TLSFallback are the Config's fields of those names, for
	// this host: its certificate is checked against its own name.
	TLSConfig   *tls.Config
	TLSFallback bool
}
