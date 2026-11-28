// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"fmt"
	"net"
	"strconv"
	"time"
)

// transport reads the settings about the connection itself rather than the
// server: keepalives, tcp_user_timeout, load_balance_hosts, requirepeer,
// sslcertmode=require, sslkeylogfile, and fallback_application_name, which applies when
// application_name is not given.
func (c *Config) transport(settings map[string]string) error {
	if err := c.keepAlive(settings); err != nil {
		return err
	}
	timeout, err := milliseconds(settings["tcp_user_timeout"], "tcp_user_timeout")
	if err != nil {
		return err
	}
	c.TCPUserTimeout = timeout
	if err := c.loadBalance(settings["load_balance_hosts"]); err != nil {
		return err
	}
	c.RequirePeer = settings["requirepeer"]
	c.RequireClientCert = settings["sslcertmode"] == "require"
	c.SSLKeyLogFile = settings["sslkeylogfile"]
	if _, named := settings["application_name"]; !named && settings["fallback_application_name"] != "" {
		settings["application_name"] = settings["fallback_application_name"]
	}
	return nil
}

// keepAlive reads keepalives, 1 or 0, and the idle time, interval and count
// of the probes. Where none of them is given the default dialer's
// keepalive stands.
func (c *Config) keepAlive(settings map[string]string) error {
	keys := []string{"keepalives", "keepalives_idle", "keepalives_interval", "keepalives_count"}
	if !anySet(settings, keys) {
		return nil
	}
	// Negative leaves the system's own value, as libpq does for a setting
	// not given.
	config := &net.KeepAliveConfig{Enable: true, Idle: -1, Interval: -1, Count: -1}
	switch settings["keepalives"] {
	case "", "1":
	case "0":
		config.Enable = false
	default:
		return fmt.Errorf("%w: keepalives is not 0 or 1", ErrInvalidConnString)
	}
	var err error
	if config.Idle, err = seconds(settings["keepalives_idle"], "keepalives_idle", config.Idle); err != nil {
		return err
	}
	if config.Interval, err = seconds(settings["keepalives_interval"], "keepalives_interval", config.Interval); err != nil {
		return err
	}
	if config.Count, err = count(settings["keepalives_count"], "keepalives_count", config.Count); err != nil {
		return err
	}
	if config.Count == 0 {
		config.Count = -1
	}
	c.KeepAlive = config
	return nil
}

// loadBalance reads load_balance_hosts: disable, the default, or random.
func (c *Config) loadBalance(mode string) error {
	switch mode {
	case "", "disable":
		return nil
	case "random":
		c.LoadBalanceHosts = true
		return nil
	}
	return fmt.Errorf("%w: load_balance_hosts %q is not one of disable, random", ErrInvalidConnString, mode)
}

func anySet(settings map[string]string, keys []string) bool {
	for _, key := range keys {
		if settings[key] != "" {
			return true
		}
	}
	return false
}

// seconds reads a whole number of seconds, or gives unset for none.
func seconds(text, key string, unset time.Duration) (time.Duration, error) {
	n, err := count(text, key, 0)
	if err != nil || n == 0 {
		// Zero is the system's default, as in libpq.
		return unset, err
	}
	return time.Duration(n) * time.Second, nil
}

// milliseconds reads a whole number of milliseconds, zero for none.
func milliseconds(text, key string) (time.Duration, error) {
	n, err := count(text, key, 0)
	return time.Duration(n) * time.Millisecond, err
}

// count reads a number that is not negative, or gives unset for none.
func count(text, key string, unset int) (int, error) {
	if text == "" {
		return unset, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: %s is not a whole number of at least 0", ErrInvalidConnString, key)
	}
	return n, nil
}
