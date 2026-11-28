// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// gssEncRequestCode asks the server for GSSAPI encryption, as sslRequestCode
// asks for TLS.
const gssEncRequestCode = 80877104

// ErrGSSEncRefused is a server that would not encrypt with GSSAPI for
// gssencmode=require: one built without it, or whose pg_hba.conf has no
// host or hostgssenc line for the connection.
var ErrGSSEncRefused = errors.New("caduceus/wire: the server refused GSSAPI encryption")

// gssEncWanted reports whether to ask for GSSAPI encryption: for require,
// and for prefer where there is a provider, over TCP. A Unix socket is
// never encrypted, as in libpq.
func (c *Conn) gssEncWanted() (bool, error) {
	mode := c.config.GSSEncMode
	switch {
	case mode == "require" && c.config.unix():
		return false, fmt.Errorf("%w: GSSAPI encryption is not to be had over a Unix socket", ErrGSSEncRefused)
	case mode == "require" && c.config.NewGSS == nil:
		return false, ErrNoGSS
	case mode == "require":
		return true, nil
	}
	return mode == "prefer" && c.config.NewGSS != nil && !c.config.unix(), nil
}

// encryptGSS asks for GSSAPI encryption where gssencmode wants it, and
// reports whether the connection is now encrypted. A server that declines
// leaves the connection as it was, for TLS or plaintext to go on from, unless
// encryption was required. So does a provider with no credentials to offer,
// for prefer: it is found out before anything is sent, as libpq finds out.
func (c *Conn) encryptGSS() (bool, error) {
	wanted, err := c.gssEncWanted()
	if err != nil || !wanted {
		return false, err
	}
	required := c.config.GSSEncMode == "require"
	wrapper, token, err := c.prepareGSS()
	if err != nil && required {
		return false, err
	}
	if err != nil {
		return false, nil
	}
	request := binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, 8), gssEncRequestCode)
	if _, err := c.netConn.Write(request); err != nil {
		return false, err
	}
	var answer [1]byte
	if _, err := io.ReadFull(c.netConn, answer[:]); err != nil {
		return false, err
	}
	switch {
	case answer[0] == 'G':
		return true, c.establishGSS(wrapper, token)
	case answer[0] == 'N' && required:
		return false, ErrGSSEncRefused
	case answer[0] == 'N':
		return false, nil
	}
	// A server before PostgreSQL 12 answers with an error and hangs up.
	return false, fmt.Errorf("%w: the server answered the request with %q", ErrGSSEncRefused, answer[0])
}

// prepareGSS makes the provider and its first token.
func (c *Conn) prepareGSS() (GSSWrapper, []byte, error) {
	gss, err := c.config.NewGSS()
	if err != nil {
		return nil, nil, err
	}
	wrapper, ok := gss.(GSSWrapper)
	if !ok {
		return nil, nil, fmt.Errorf("%w: the GSS provider cannot encrypt: it is no wire.GSSWrapper", ErrGSSEncRefused)
	}
	token, err := gssInitToken(c.config, gss)
	return wrapper, token, err
}

// establishGSS makes the GSSAPI context, its tokens framed as the
// encryption's packets are, and wraps the connection in it.
func (c *Conn) establishGSS(wrapper GSSWrapper, token []byte) error {
	for done := false; !done; {
		if err := c.sendGSSPacket(token); err != nil {
			return err
		}
		reply, err := c.readGSSPacket()
		if err != nil {
			return err
		}
		if done, token, err = wrapper.Continue(reply); err != nil {
			return err
		}
		if done && len(token) > 0 {
			if err := c.sendGSSPacket(token); err != nil {
				return err
			}
		}
	}
	c.netConn = &gssConn{Conn: c.netConn, wrapper: wrapper}
	return nil
}

func (c *Conn) sendGSSPacket(token []byte) error {
	packet := binary.BigEndian.AppendUint32(nil, uint32(len(token)))
	_, err := c.netConn.Write(append(packet, token...))
	return err
}

// readGSSPacket reads a token of the context's making. A server that fails
// it sends an ErrorResponse instead, whose first byte is E where a length's
// would be zero.
func (c *Conn) readGSSPacket() ([]byte, error) {
	var length [4]byte
	if _, err := io.ReadFull(c.netConn, length[:]); err != nil {
		return nil, err
	}
	if length[0] == MsgErrorResponse {
		return nil, c.gssError(length[1:])
	}
	size := binary.BigEndian.Uint32(length[:])
	if size == 0 || size > gssPacketSize {
		return nil, fmt.Errorf("%w: a GSSAPI token of %d bytes", ErrProtocol, size)
	}
	token := make([]byte, size)
	_, err := io.ReadFull(c.netConn, token)
	return token, err
}

// gssError reads the ErrorResponse that began with head.
func (c *Conn) gssError(head []byte) error {
	var rest [1]byte
	if _, err := io.ReadFull(c.netConn, rest[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(append(head[:3:3], rest[0]))
	if size < 4 || size > gssPacketSize {
		return ErrProtocol
	}
	body := make([]byte, size-4)
	if _, err := io.ReadFull(c.netConn, body); err != nil {
		return err
	}
	return parseError(body)
}

// gssEncMode reads gssencmode.
func (c *Config) gssEncMode(mode string) error {
	switch mode {
	case "", "disable", "prefer", "require":
		c.GSSEncMode = mode
		return nil
	}
	return fmt.Errorf("%w: gssencmode %q is not one of disable, prefer, require", ErrInvalidConnString, mode)
}
