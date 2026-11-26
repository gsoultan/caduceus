// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

// StartOptions are how StartReplication streams.
//
// Physical streams WAL as it is, from a physical slot or, with no slot,
// none; Timeline is the timeline to stream, the current one for zero.
// PluginArgs are a logical slot's plugin options, each a name and its value
// as the replication grammar writes it, such as `proto_version '2'`;
// PgoutputOptions builds them for pgoutput.
type StartOptions struct {
	Physical   bool
	Timeline   int32
	PluginArgs []string
}
