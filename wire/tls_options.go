// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"crypto/tls"
	"fmt"
)

// tlsVersionNames are the versions ssl_min_protocol_version and
// ssl_max_protocol_version take, as libpq spells them. TLS 1.0 and 1.1 are
// accepted as libpq accepts them; Go's TLS refuses to offer them below its
// own floor unless the program allows it.
var tlsVersionNames = map[string]uint16{
	"TLSv1": tls.VersionTLS10, "TLSv1.1": tls.VersionTLS11,
	"TLSv1.2": tls.VersionTLS12, "TLSv1.3": tls.VersionTLS13,
}

// tlsVersions reads ssl_min_protocol_version, TLSv1.2 when not given, as in
// libpq, and ssl_max_protocol_version, which bounds nothing when not given.
func tlsVersions(config *tls.Config, settings map[string]string) error {
	for _, bound := range []struct {
		key    string
		target *uint16
	}{
		{"ssl_min_protocol_version", &config.MinVersion},
		{"ssl_max_protocol_version", &config.MaxVersion},
	} {
		if err := tlsVersion(settings[bound.key], bound.key, bound.target); err != nil {
			return err
		}
	}
	if config.MaxVersion != 0 && config.MaxVersion < config.MinVersion {
		return fmt.Errorf("%w: ssl_max_protocol_version is below ssl_min_protocol_version", ErrInvalidConnString)
	}
	return nil
}

// tlsVersion sets target from one version's name, leaving it as it is for
// an empty one.
func tlsVersion(name, key string, target *uint16) error {
	if name == "" {
		return nil
	}
	version, ok := tlsVersionNames[name]
	if !ok {
		return fmt.Errorf("%w: %s %q is not one of TLSv1, TLSv1.1, TLSv1.2, TLSv1.3", ErrInvalidConnString, key, name)
	}
	*target = version
	return nil
}

// clientCertificate loads sslcert and sslkey unless sslcertmode=disable,
// which sends no certificate even to a server that asks for one.
func clientCertificate(config *tls.Config, settings map[string]string) error {
	mode := settings["sslcertmode"]
	switch mode {
	case "disable":
		return nil
	case "", "allow", "require":
		return loadClientCertificate(config, settings["sslcert"], settings["sslkey"], settings["sslpassword"])
	}
	return fmt.Errorf("%w: sslcertmode %q is not one of disable, allow, require", ErrInvalidConnString, mode)
}
