// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

// Package wire implements the PostgreSQL frontend/backend
// protocol, version 3.0: connection strings, TLS negotiation, authentication,
// message framing, server errors, cancellation and notifications.
//
// It knows nothing about Go types. A connection moves messages; package caduceus
// builds queries out of them, and package replication decodes a replication
// stream.
// It has no dependency outside the standard library.
//
// A Conn is owned by one goroutine at a time. Nothing in it is locked, because
// the protocol is a strict request/response sequence and two goroutines sharing
// one connection would interleave messages whatever this package did.
package wire
