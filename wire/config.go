// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"crypto/tls"
	"maps"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultMaxMessageSize is the largest message a connection accepts unless told
// otherwise. PostgreSQL's own ceiling on a single field is a gigabyte, so a
// limit below that would refuse rows the server is happy to send.
const DefaultMaxMessageSize = 1<<30 + 1<<20

// Config is everything needed to open a connection, resolved once from a
// connection string by ParseConfig. Opening many connections from one Config
// re-reads nothing: no environment, no password file, no certificate.
type Config struct {
	// Host is a hostname, an IP address, or the directory of a Unix socket
	// (any value starting with a slash).
	Host string
	Port uint16

	User     string
	Password string
	Database string

	// RuntimeParams are sent in the startup packet: application_name,
	// search_path, options, replication and anything else the server accepts
	// there.
	RuntimeParams map[string]string

	// TLSConfig is nil when the connection is not to be encrypted.
	TLSConfig *tls.Config
	// TLSFallback allows a plaintext connection when the server declines TLS or
	// the handshake fails: libpq's sslmode=prefer, the default.
	TLSFallback bool

	// ConnectTimeout bounds dialling and logging in when the caller's context
	// has no earlier deadline. Zero means only the context bounds it.
	ConnectTimeout time.Duration

	// MaxMessageSize bounds a single message from the server. Zero means
	// DefaultMaxMessageSize.
	MaxMessageSize int

	// NoStreamWatermark leaves the socket's receive low-water mark alone.
	// By default, on Linux over TCP, it is raised to 64 KiB once a result has
	// run past 4 MiB, which wakes the reader an eighth as often; the end of
	// such a result is then delivered about 2 ms late, or later where the
	// machine is too busy to run the timer that lowers the mark on time. The
	// kernel is asked whether it wakes a reader when the mark is lowered by
	// its version, so a system that emulates Linux and reports a Linux
	// version, such as gVisor or WSL 1, is taken at its word: set this there
	// if long results stall at their end.
	NoStreamWatermark bool

	// MinProtocolVersion and MaxProtocolVersion bound the protocol version a
	// connection uses, as min_protocol_version and max_protocol_version do.
	// Zero means Protocol30 and Protocol32: the connection asks for 3.2,
	// which PostgreSQL 18 and later use, and an older server answers that it
	// speaks 3.0, which the connection then uses. A pooler that refuses 3.2
	// outright, as PgBouncer before 1.24 does, is asked again for 3.0, and
	// every later connection made from this Config, or a copy of it, asks
	// for 3.0 at once. A MinProtocolVersion of Protocol32 refuses any server
	// that does not speak 3.2.
	MinProtocolVersion ProtocolVersion
	MaxProtocolVersion ProtocolVersion

	// downgraded is shared by a Config and its copies: set once a server
	// refused 3.2, so the connections after it do not ask again.
	downgraded *atomic.Bool
}

// Copy returns a deep copy, safe to adjust for one connection without
// affecting the template.
func (c *Config) Copy() *Config {
	clone := *c
	clone.RuntimeParams = maps.Clone(c.RuntimeParams)
	if c.TLSConfig != nil {
		clone.TLSConfig = c.TLSConfig.Clone()
	}
	return &clone
}

// minProtocol is the oldest protocol version a connection may use.
func (c *Config) minProtocol() ProtocolVersion {
	if c.MinProtocolVersion == 0 {
		return Protocol30
	}
	return c.MinProtocolVersion
}

// firstProtocol is the version a connection asks for first: the newest
// allowed, unless a server already refused it.
func (c *Config) firstProtocol() ProtocolVersion {
	if c.downgraded != nil && c.downgraded.Load() && c.minProtocol() == Protocol30 {
		return Protocol30
	}
	if c.MaxProtocolVersion == 0 {
		return Protocol32
	}
	return c.MaxProtocolVersion
}

// downgrade records that a server refused a newer protocol than 3.0.
func (c *Config) downgrade() {
	if c.downgraded != nil {
		c.downgraded.Store(true)
	}
}

// unix reports whether Host names a socket directory.
func (c *Config) unix() bool {
	return strings.HasPrefix(c.Host, "/")
}

// target returns the network and address to dial.
func (c *Config) target() (string, string) {
	port := strconv.Itoa(int(c.Port))
	if c.unix() {
		return "unix", c.Host + "/.s.PGSQL." + port
	}
	return "tcp", net.JoinHostPort(c.Host, port)
}

// noWatermark reports whether the low-water mark is to be left alone.
func (c *Config) noWatermark() bool {
	return c == nil || c.NoStreamWatermark
}

func (c *Config) maxMessage() int {
	if c.MaxMessageSize > 0 {
		return c.MaxMessageSize
	}
	return DefaultMaxMessageSize
}
