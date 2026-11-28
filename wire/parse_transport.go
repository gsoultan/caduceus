// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"fmt"
	"strconv"
	"time"
)

// transport reads the settings about the connection itself rather than the
// server: keepalives, tcp_user_timeout, load_balance_hosts, requirepeer,
// sslcertmode, sslkeylogfile, and fallback_application_name, which applies when
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
	c.SSLCertMode = settings["sslcertmode"]
	c.SSLKeyLogFile = settings["sslkeylogfile"]
	if _, named := settings["application_name"]; !named && settings["fallback_application_name"] != "" {
		settings["application_name"] = settings["fallback_application_name"]
	}
	return nil
}

// keepAlive reads keepalives, 1 or 0, and the idle time, interval and count
// of the probes. Zero or none leaves the dialer's default for each.
func (c *Config) keepAlive(settings map[string]string) error {
	switch settings["keepalives"] {
	case "", "1":
	case "0":
		c.NoKeepAlive = true
	default:
		return fmt.Errorf("%w: keepalives is not 0 or 1", ErrInvalidConnString)
	}
	var err error
	if c.KeepAlive.Idle, err = seconds(settings["keepalives_idle"], "keepalives_idle"); err != nil {
		return err
	}
	if c.KeepAlive.Interval, err = seconds(settings["keepalives_interval"], "keepalives_interval"); err != nil {
		return err
	}
	c.KeepAlive.Count, err = count(settings["keepalives_count"], "keepalives_count", 0)
	return err
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

// seconds reads a whole number of seconds, zero for none.
func seconds(text, key string) (time.Duration, error) {
	n, err := count(text, key, 0)
	return time.Duration(n) * time.Second, err
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
