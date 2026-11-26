// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/gsoultan/caduceus/wire"
)

// relayedConn connects to the server of DATABASE_URL through a relay on the
// loopback, which hands every piece either side sends to fromClient or
// fromServer before passing it on. What a driver puts on the wire, and when,
// is not something its results show.
func relayedConn(t *testing.T, options Options, fromClient, fromServer func(piece []byte)) *Conn {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	config, err := wire.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig() = %v", err)
	}
	if len(config.Host) > 0 && config.Host[0] == '/' {
		t.Skip("the relay is a TCP one")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	server := net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port)))
	go func() {
		client, err := listener.Accept()
		if err != nil {
			return
		}
		upstream, err := net.Dial("tcp", server)
		if err != nil {
			_ = client.Close()
			return
		}
		go relay(client, upstream, fromServer)
		relay(upstream, client, fromClient)
	}()

	relayed := config.Copy()
	relayed.Host = "127.0.0.1"
	relayed.Port = uint16(listener.Addr().(*net.TCPAddr).Port)
	conn, err := ConnectConfig(t.Context(), relayed, options)
	if err != nil {
		t.Fatalf("ConnectConfig() through the relay = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// relay copies what from sends to to, handing each piece to saw before it goes
// on, and closes to when from is done.
func relay(to, from net.Conn, saw func(piece []byte)) {
	defer to.Close()
	buf := make([]byte, 64<<10)
	for {
		n, err := from.Read(buf)
		if n > 0 {
			saw(buf[:n])
			if _, werr := to.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// countedConn connects through a relay that counts round trips: the times the
// client speaks after the server last did. It returns the connection and a
// function that returns the count so far and starts it again.
func countedConn(t *testing.T, options Options) (*Conn, func() int) {
	t.Helper()
	var mu sync.Mutex
	trips, clientSpoke := 0, false
	conn := relayedConn(t, options, func([]byte) {
		mu.Lock()
		if !clientSpoke {
			trips, clientSpoke = trips+1, true
		}
		mu.Unlock()
	}, func([]byte) {
		mu.Lock()
		clientSpoke = false
		mu.Unlock()
	})
	return conn, func() int {
		mu.Lock()
		defer mu.Unlock()
		counted := trips
		trips = 0
		return counted
	}
}

// sentMessage is a message the client sent: its type, and for a Parse, a Bind,
// a Describe or a Close the statement it names.
type sentMessage struct {
	kind      byte
	statement string
}

// watchedConn connects through a relay that keeps what the client sends. It
// returns the connection and a function that returns the messages sent since
// it was last called, which is everything an exchange sent once the exchange
// has been answered.
func watchedConn(t *testing.T, options Options) (*Conn, func() []sentMessage) {
	t.Helper()
	var mu sync.Mutex
	var sent []byte
	started := false
	conn := relayedConn(t, options, func(piece []byte) {
		mu.Lock()
		sent = append(sent, piece...)
		mu.Unlock()
	}, func([]byte) {})
	return conn, func() []sentMessage {
		mu.Lock()
		defer mu.Unlock()
		if !started {
			sent, started = afterStartup(t, sent), true
		}
		messages, rest := splitSent(sent)
		sent = rest
		return messages
	}
}

// afterStartup drops what a client sends before its first typed message: a
// request for TLS or GSS encryption, if it makes one, and the startup message,
// none of which has a type byte.
func afterStartup(t *testing.T, sent []byte) []byte {
	t.Helper()
	for len(sent) >= 8 {
		length, code := int(binary.BigEndian.Uint32(sent)), binary.BigEndian.Uint32(sent[4:])
		if length > len(sent) {
			break
		}
		sent = sent[length:]
		if code>>16 == 3 {
			return sent
		}
	}
	t.Fatal("the relay saw no startup message; over TLS it cannot read what is sent")
	return nil
}

// splitSent splits typed messages off the front of what a client sent, and
// returns the incomplete one at the end, if there is one.
func splitSent(sent []byte) ([]sentMessage, []byte) {
	var messages []sentMessage
	for len(sent) >= 5 {
		length := int(binary.BigEndian.Uint32(sent[1:]))
		if 1+length > len(sent) {
			break
		}
		message, body := sentMessage{kind: sent[0]}, sent[5:1+length]
		switch message.kind {
		case 'B':
			// The portal's name comes first, then the statement's.
			_, body = cutName(body)
			message.statement, _ = cutName(body)
		case 'D', 'C':
			if len(body) > 0 && body[0] == 'S' {
				message.statement, _ = cutName(body[1:])
			}
		case 'P':
			message.statement, _ = cutName(body)
		}
		messages = append(messages, message)
		sent = sent[1+length:]
	}
	return messages, sent
}

func cutName(body []byte) (string, []byte) {
	for i, c := range body {
		if c == 0 {
			return string(body[:i]), body[i+1:]
		}
	}
	return string(body), nil
}
