// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"crypto/tls"
	"fmt"
	"os"
	"strconv"
	"time"
)

// socket reads the settings that shape the connection rather than the
// session: keepalives, tcp_user_timeout, load_balance_hosts, requirepeer and
// sslcertmode.
func (c *Config) socket(settings map[string]string) error {
	switch settings["keepalives"] {
	case "", "1":
	case "0":
		c.NoKeepAlive = true
	default:
		return fmt.Errorf("%w: keepalives is not 0 or 1", ErrInvalidConnString)
	}
	var err error
	if c.KeepAlive.Idle, err = seconds(settings, "keepalives_idle"); err != nil {
		return err
	}
	if c.KeepAlive.Interval, err = seconds(settings, "keepalives_interval"); err != nil {
		return err
	}
	if text := settings["keepalives_count"]; text != "" {
		if c.KeepAlive.Count, err = strconv.Atoi(text); err != nil || c.KeepAlive.Count < 0 {
			return fmt.Errorf("%w: keepalives_count is not a count", ErrInvalidConnString)
		}
	}
	if text := settings["tcp_user_timeout"]; text != "" {
		millis, err := strconv.Atoi(text)
		if err != nil || millis < 0 {
			return fmt.Errorf("%w: tcp_user_timeout is not a number of milliseconds", ErrInvalidConnString)
		}
		c.TCPUserTimeout = time.Duration(millis) * time.Millisecond
	}
	switch settings["load_balance_hosts"] {
	case "", "disable":
	case "random":
		c.LoadBalanceHosts = true
	default:
		return fmt.Errorf("%w: load_balance_hosts is not one of disable, random", ErrInvalidConnString)
	}
	c.RequirePeer = settings["requirepeer"]
	switch c.SSLCertMode = settings["sslcertmode"]; c.SSLCertMode {
	case "", "allow", "disable", "require":
	default:
		return fmt.Errorf("%w: sslcertmode is not one of disable, allow, require", ErrInvalidConnString)
	}
	return nil
}

// seconds reads a whole number of seconds; empty is zero.
func seconds(settings map[string]string, key string) (time.Duration, error) {
	text := settings[key]
	if text == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: %s is not a number of seconds", ErrInvalidConnString, key)
	}
	return time.Duration(n) * time.Second, nil
}

// tlsVersions reads ssl_min_protocol_version and ssl_max_protocol_version.
// libpq's default minimum, TLSv1.2, is kept when the first is not given.
func tlsVersions(config *tls.Config, settings map[string]string) error {
	versions := map[string]uint16{"TLSv1": tls.VersionTLS10, "TLSv1.1": tls.VersionTLS11, "TLSv1.2": tls.VersionTLS12, "TLSv1.3": tls.VersionTLS13}
	for key, dst := range map[string]*uint16{"ssl_min_protocol_version": &config.MinVersion, "ssl_max_protocol_version": &config.MaxVersion} {
		text := settings[key]
		if text == "" {
			continue
		}
		version, ok := versions[text]
		if !ok {
			return fmt.Errorf("%w: %s is not one of TLSv1, TLSv1.1, TLSv1.2, TLSv1.3", ErrInvalidConnString, key)
		}
		*dst = version
	}
	if config.MaxVersion != 0 && config.MinVersion > config.MaxVersion {
		return fmt.Errorf("%w: ssl_min_protocol_version is newer than ssl_max_protocol_version", ErrInvalidConnString)
	}
	return nil
}

// keyLog appends the TLS session keys to the file sslkeylogfile names, in the
// NSS format Wireshark reads, for debugging an encrypted connection.
func keyLog(config *tls.Config, file string) error {
	if file == "" {
		return nil
	}
	w, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("%w: opening sslkeylogfile: %w", ErrInvalidConnString, err)
	}
	config.KeyLogWriter = w
	return nil
}
