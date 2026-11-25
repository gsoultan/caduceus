// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

// Package replication decodes PostgreSQL's streaming replication protocol and
// the pgoutput logical decoding plugin (protocol version 1).
//
// It holds no connection. Start a stream with wire.Conn.StartCopyBoth, feed
// each CopyData body to ParseXLogData or ParseKeepalive by its first byte, hand
// each XLogData record to a Decoder, and confirm progress with
// AppendStandbyStatus wrapped in wire.AppendCopyData.
package replication
