// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"
)

// ParseConfig resolves a connection string, in URL form
// (postgres://user:password@host:port/database?sslmode=require) or keyword/value
// form (host=db user=app sslmode=require), the way libpq does for a single host.
//
// What the string leaves out is taken from the PG* environment variables libpq
// reads — PGHOST, PGPORT, PGUSER, PGPASSWORD, PGDATABASE, PGSSLMODE,
// PGSSLROOTCERT, PGSSLCERT, PGSSLKEY, PGAPPNAME, PGCONNECT_TIMEOUT, PGPASSFILE —
// and a password from the password file. Everything is read here, once; a
// Config never goes back to the environment.
//
// Not supported, and refused rather than ignored: several hosts in one string,
// and service files. Settings that only pgx understood (statement_cache_capacity,
// default_query_exec_mode, pool_*) are dropped, so a string written for pgx
// still connects.
func ParseConfig(connString string) (*Config, error) {
	settings, err := parseSettings(connString)
	if err != nil {
		return nil, err
	}
	addEnvironment(settings)
	return buildConfig(settings)
}

// connectionKeys describe the connection rather than the session, so they are
// not sent to the server.
var connectionKeys = map[string]bool{
	"host": true, "port": true, "user": true, "password": true, "dbname": true,
	"sslmode": true, "sslrootcert": true, "sslcert": true, "sslkey": true,
	"sslsni": true, "sslpassword": true, "connect_timeout": true, "passfile": true,
	"target_session_attrs": true, "hostaddr": true,
	"min_protocol_version": true, "max_protocol_version": true,
}

// clientOnlyKeys are settings pgx read from a connection string. Passing them on
// would make the server refuse the connection as an unrecognised parameter.
var clientOnlyKeys = map[string]bool{
	"statement_cache_capacity": true, "description_cache_capacity": true,
	"default_query_exec_mode": true, "min_read_buffer_size": true,
	"pool_max_conns": true, "pool_min_conns": true, "pool_max_conn_lifetime": true,
	"pool_max_conn_idle_time": true, "pool_health_check_period": true,
	"pool_max_conn_lifetime_jitter": true, "pool_min_idle_conns": true,
}

var environment = [][2]string{
	{"PGHOST", "host"}, {"PGPORT", "port"}, {"PGDATABASE", "dbname"},
	{"PGUSER", "user"}, {"PGPASSWORD", "password"}, {"PGSSLMODE", "sslmode"},
	{"PGSSLROOTCERT", "sslrootcert"}, {"PGSSLCERT", "sslcert"},
	{"PGSSLKEY", "sslkey"}, {"PGAPPNAME", "application_name"},
	{"PGCONNECT_TIMEOUT", "connect_timeout"}, {"PGPASSFILE", "passfile"},
	{"PGTARGETSESSIONATTRS", "target_session_attrs"},
	{"PGMINPROTOCOLVERSION", "min_protocol_version"},
	{"PGMAXPROTOCOLVERSION", "max_protocol_version"},
}

func parseSettings(text string) (map[string]string, error) {
	if strings.HasPrefix(text, "postgres://") || strings.HasPrefix(text, "postgresql://") {
		return parseURL(text)
	}
	return parseKeywords(text)
}

func parseURL(text string) (map[string]string, error) {
	text, hostPart := cutHosts(text)
	parsed, err := url.Parse(text)
	if err != nil {
		return nil, ErrInvalidConnString
	}
	parsed.Host = hostPart
	settings := map[string]string{}
	if parsed.User != nil {
		settings["user"] = parsed.User.Username()
		if password, ok := parsed.User.Password(); ok {
			settings["password"] = password
		}
	}
	if hosts, ports := splitHosts(parsed.Host); hosts != "" {
		settings["host"] = hosts
		if strings.Trim(ports, ",") != "" {
			settings["port"] = ports
		}
	}
	if database := strings.TrimPrefix(parsed.Path, "/"); database != "" {
		settings["dbname"] = database
	}
	for key, values := range parsed.Query() {
		settings[key] = values[len(values)-1]
	}
	return settings, nil
}

// parseKeywords reads libpq's keyword/value form: key=value pairs separated by
// whitespace, a value optionally single-quoted, with backslash escaping a quote
// or a backslash.
func parseKeywords(text string) (map[string]string, error) {
	settings := map[string]string{}
	for text = strings.TrimSpace(text); text != ""; text = strings.TrimSpace(text) {
		equals := strings.IndexByte(text, '=')
		if equals <= 0 {
			return nil, ErrInvalidConnString
		}
		key := strings.TrimSpace(text[:equals])
		value, rest, err := keywordValue(strings.TrimLeft(text[equals+1:], " \t\n\r"))
		if err != nil {
			return nil, err
		}
		settings[key] = value
		text = rest
	}
	return settings, nil
}

func keywordValue(text string) (string, string, error) {
	var value strings.Builder
	quoted := strings.HasPrefix(text, "'")
	if quoted {
		text = text[1:]
	}
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '\\' && i+1 < len(text):
			i++
			value.WriteByte(text[i])
		case quoted && c == '\'':
			return value.String(), text[i+1:], nil
		case !quoted && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			return value.String(), text[i:], nil
		default:
			value.WriteByte(c)
		}
	}
	if quoted {
		return "", "", fmt.Errorf("%w: unterminated quoted value", ErrInvalidConnString)
	}
	return value.String(), "", nil
}

func addEnvironment(settings map[string]string) {
	for _, pair := range environment {
		if _, set := settings[pair[1]]; set {
			continue
		}
		if value, ok := os.LookupEnv(pair[0]); ok {
			settings[pair[1]] = value
		}
	}
}

func buildConfig(settings map[string]string) (*Config, error) {
	config := &Config{RuntimeParams: map[string]string{}}
	if err := config.address(settings); err != nil {
		return nil, err
	}
	if err := config.identity(settings); err != nil {
		return nil, err
	}
	if err := config.timeout(settings["connect_timeout"]); err != nil {
		return nil, err
	}
	if err := config.protocol(settings); err != nil {
		return nil, err
	}
	if err := config.sessionAttrs(settings["target_session_attrs"]); err != nil {
		return nil, err
	}
	for key, value := range settings {
		if !connectionKeys[key] && !clientOnlyKeys[key] {
			config.RuntimeParams[key] = value
		}
	}
	if err := config.encryption(settings); err != nil {
		return nil, err
	}
	return config, nil
}

func (c *Config) address(settings map[string]string) error {
	hosts := strings.Split(settings["host"], ",")
	ports := strings.Split(settings["port"], ",")
	if len(ports) != 1 && len(ports) != len(hosts) {
		return fmt.Errorf("%w: %d ports for %d hosts", ErrInvalidConnString, len(ports), len(hosts))
	}
	for i, host := range hosts {
		port, err := parsePort(ports[min(i, len(ports)-1)])
		if err != nil {
			return err
		}
		if host == "" {
			host = defaultHost(port)
		}
		if i == 0 {
			c.Host, c.Port = host, port
			continue
		}
		c.Fallbacks = append(c.Fallbacks, &FallbackConfig{Host: host, Port: port})
	}
	return nil
}

// parsePort reads a port number, 5432 if none is given.
func parsePort(text string) (uint16, error) {
	if text == "" {
		return 5432, nil
	}
	number, err := strconv.ParseUint(text, 10, 16)
	if err != nil || number == 0 {
		return 0, fmt.Errorf("%w: port is not a port number", ErrInvalidConnString)
	}
	return uint16(number), nil
}

// cutHosts takes the host part out of a URL, which url.Parse refuses when it
// lists several hosts, IPv6 among them, and returns the URL without it.
func cutHosts(text string) (string, string) {
	scheme, rest, found := strings.Cut(text, "://")
	if !found {
		return text, ""
	}
	end := strings.IndexAny(rest, "/?")
	if end < 0 {
		end = len(rest)
	}
	authority := rest[:end]
	at := strings.LastIndex(authority, "@")
	return scheme + "://" + authority[:at+1] + rest[end:], authority[at+1:]
}

// splitHosts splits a URL's host part, host:port,host:port, into a list of
// hosts and a list of ports, either of which may have empty entries, as
// key=value strings give them. A host in brackets is an IPv6 address, and one
// percent-encoded, such as %2Fvar%2Frun%2Fpostgresql, a socket directory.
func splitHosts(text string) (string, string) {
	if text == "" {
		return "", ""
	}
	var hosts, ports []string
	for _, part := range strings.Split(text, ",") {
		host, port := splitHostPort(part)
		if unescaped, err := url.PathUnescape(host); err == nil {
			host = unescaped
		}
		hosts, ports = append(hosts, host), append(ports, port)
	}
	return strings.Join(hosts, ","), strings.Join(ports, ",")
}

// splitHostPort splits host:port, or [v6]:port, with the port optional.
func splitHostPort(text string) (string, string) {
	if rest, ok := strings.CutPrefix(text, "["); ok {
		host, port, _ := strings.Cut(rest, "]")
		return host, strings.TrimPrefix(port, ":")
	}
	host, port, _ := strings.Cut(text, ":")
	return host, port
}

// sessionAttrs sets ValidateConnect for target_session_attrs.
func (c *Config) sessionAttrs(attrs string) error {
	validators := map[string]func(context.Context, *Conn) error{
		"read-write":     ValidateReadWrite,
		"read-only":      ValidateReadOnly,
		"primary":        ValidatePrimary,
		"standby":        ValidateStandby,
		"prefer-standby": ValidatePreferStandby,
	}
	if attrs == "" || attrs == "any" {
		return nil
	}
	validate, ok := validators[attrs]
	if !ok {
		return fmt.Errorf("%w: target_session_attrs %q is not one of any, read-write, read-only, primary, standby, prefer-standby", ErrInvalidConnString, attrs)
	}
	c.ValidateConnect = validate
	return nil
}

// defaultHost is a local socket when the server is listening on one, as libpq
// prefers, and localhost otherwise.
func defaultHost(port uint16) string {
	for _, dir := range []string{"/var/run/postgresql", "/private/tmp", "/tmp"} {
		if _, err := os.Stat(dir + "/.s.PGSQL." + strconv.Itoa(int(port))); err == nil {
			return dir
		}
	}
	return "localhost"
}

func (c *Config) identity(settings map[string]string) error {
	c.User = settings["user"]
	if c.User == "" {
		current, err := user.Current()
		if err != nil {
			return fmt.Errorf("%w: no user given and the current one is unknown", ErrInvalidConnString)
		}
		c.User = current.Username
	}
	c.Database = settings["dbname"]
	if c.Database == "" {
		c.Database = c.User
	}
	c.Password = settings["password"]
	if _, given := settings["password"]; !given {
		c.Password = passwordFromFile(settings["passfile"], c)
	}
	return nil
}

// protocol reads the bounds on the protocol version.
func (c *Config) protocol(settings map[string]string) error {
	var err error
	if c.MinProtocolVersion, err = parseProtocolVersion("min_protocol_version", settings["min_protocol_version"]); err != nil {
		return err
	}
	if c.MaxProtocolVersion, err = parseProtocolVersion("max_protocol_version", settings["max_protocol_version"]); err != nil {
		return err
	}
	if c.minProtocol() > c.firstProtocol() {
		return fmt.Errorf("%w: min_protocol_version is newer than max_protocol_version", ErrInvalidConnString)
	}
	return nil
}

func (c *Config) timeout(text string) error {
	if text == "" {
		return nil
	}
	seconds, err := strconv.Atoi(text)
	if err != nil || seconds < 0 {
		return fmt.Errorf("%w: connect_timeout is not a number of seconds", ErrInvalidConnString)
	}
	c.ConnectTimeout = time.Duration(seconds) * time.Second
	return nil
}

// encryption turns sslmode and its companions into a TLS configuration, with
// libpq's meanings: prefer and require encrypt without authenticating the
// server, verify-ca checks its certificate chain, verify-full also checks its
// name. require with a root certificate given verifies the chain, as libpq does.
func (c *Config) encryption(settings map[string]string) error {
	var err error
	if c.TLSConfig, c.TLSFallback, err = hostTLS(c.Host, settings); err != nil {
		return err
	}
	for _, fallback := range c.Fallbacks {
		if fallback.TLSConfig, fallback.TLSFallback, err = hostTLS(fallback.Host, settings); err != nil {
			return err
		}
	}
	return nil
}

// hostTLS is the TLS configuration for one host, checked against its name,
// and whether plaintext is allowed if TLS is not to be had.
func hostTLS(host string, settings map[string]string) (*tls.Config, bool, error) {
	mode := settings["sslmode"]
	if mode == "" {
		mode = "prefer"
	}
	if mode == "disable" || mode == "allow" || (&Config{Host: host}).unix() {
		return nil, false, validMode(mode)
	}

	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	if settings["sslsni"] == "0" {
		config.ServerName = ""
	}
	rootFile := settings["sslrootcert"]

	fallback := false
	switch mode {
	case "prefer":
		config.InsecureSkipVerify = true
		fallback = true
	case "require":
		config.InsecureSkipVerify = rootFile == ""
	case "verify-ca", "verify-full":
	default:
		return nil, false, validMode(mode)
	}

	if err := loadRoots(config, rootFile); err != nil {
		return nil, false, err
	}
	if !config.InsecureSkipVerify && mode != "verify-full" {
		verifyChainOnly(config)
	}
	if err := loadClientCertificate(config, settings["sslcert"], settings["sslkey"]); err != nil {
		return nil, false, err
	}
	return config, fallback, nil
}

func validMode(mode string) error {
	switch mode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		return nil
	}
	return fmt.Errorf("%w: sslmode %q is not one of disable, allow, prefer, require, verify-ca, verify-full", ErrInvalidConnString, mode)
}

func loadRoots(config *tls.Config, rootFile string) error {
	if rootFile == "" || rootFile == "system" {
		return nil
	}
	pem, err := os.ReadFile(rootFile)
	if err != nil {
		return fmt.Errorf("%w: reading sslrootcert: %w", ErrInvalidConnString, err)
	}
	config.RootCAs = x509.NewCertPool()
	if !config.RootCAs.AppendCertsFromPEM(pem) {
		return fmt.Errorf("%w: sslrootcert holds no certificate", ErrInvalidConnString)
	}
	return nil
}

func loadClientCertificate(config *tls.Config, certFile, keyFile string) error {
	if (certFile == "") != (keyFile == "") {
		return fmt.Errorf("%w: sslcert and sslkey must be given together", ErrInvalidConnString)
	}
	if certFile == "" {
		return nil
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("%w: loading sslcert: %w", ErrInvalidConnString, err)
	}
	config.Certificates = []tls.Certificate{pair}
	return nil
}

// verifyChainOnly makes a configuration check that the server's certificate
// descends from a trusted root without checking the name on it: libpq's
// verify-ca. crypto/tls has no such mode, so the check is done by hand.
func verifyChainOnly(config *tls.Config) {
	roots := config.RootCAs
	config.InsecureSkipVerify = true
	config.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) == 0 {
			return errors.New("caduceus/wire: server sent no certificate")
		}
		certificates := make([]*x509.Certificate, len(raw))
		for i, der := range raw {
			certificate, err := x509.ParseCertificate(der)
			if err != nil {
				return err
			}
			certificates[i] = certificate
		}
		intermediates := x509.NewCertPool()
		for _, certificate := range certificates[1:] {
			intermediates.AddCert(certificate)
		}
		_, err := certificates[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates})
		return err
	}
}
