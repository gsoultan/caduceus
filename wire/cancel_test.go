// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package wire

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

// A cancel request goes the way the connection went, through DialFunc. It
// used to dial the address itself, which for a connection through a tunnel
// or a proxy reached nothing, or the wrong server.
func TestCancelRequestUsesDialFunc(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	dialed := ""
	config := &Config{Host: "db.internal", Port: 5432, DialFunc: func(_ context.Context, network, address string) (net.Conn, error) {
		dialed = network + " " + address
		return client, nil
	}}
	conn := &Conn{config: config, pid: 42, secret: []byte{1, 2, 3, 4}}
	got := make(chan []byte, 1)
	go func() {
		request := make([]byte, 16)
		_, _ = io.ReadFull(server, request)
		got <- request
		_ = server.Close()
	}()
	if err := conn.CancelRequest(t.Context()); err != nil {
		t.Fatal(err)
	}
	request := <-got
	if dialed != "tcp db.internal:5432" || binary.BigEndian.Uint32(request[4:]) != cancelRequestCode || binary.BigEndian.Uint32(request[8:]) != 42 {
		t.Errorf("dialed %q and sent %x", dialed, request)
	}
}
