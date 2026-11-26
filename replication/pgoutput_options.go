// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package replication

import (
	"strconv"
	"strings"
)

// PgoutputOptions are the options of the pgoutput plugin, for
// StartOptions.PluginArgs.
//
// ProtoVersion is 1 to 4, 1 for zero: 2 for Streaming (PostgreSQL 14), 3 for
// TwoPhase (15), 4 for parallel streaming (16). Messages sends what
// pg_logical_emit_message writes; Binary sends values in binary; Origin is
// "any" or "none", PostgreSQL 16 and later, to leave out changes that came
// from another origin. Streaming is "on", "off" or, with version 4,
// "parallel", "" for the server's default.
type PgoutputOptions struct {
	ProtoVersion int
	Publications []string
	Binary       bool
	Messages     bool
	Streaming    string
	TwoPhase     bool
	Origin       string
}

// Args are the options as plugin arguments, every value quoted.
func (o PgoutputOptions) Args() []string {
	version := max(o.ProtoVersion, 1)
	args := []string{"proto_version " + QuoteLiteral(strconv.Itoa(version)), "publication_names " + QuoteLiteral(strings.Join(o.Publications, ","))}
	if o.Binary {
		args = append(args, "binary 'true'")
	}
	if o.Messages {
		args = append(args, "messages 'true'")
	}
	if o.Streaming != "" {
		args = append(args, "streaming "+QuoteLiteral(o.Streaming))
	}
	if o.TwoPhase {
		args = append(args, "two_phase 'true'")
	}
	if o.Origin != "" {
		args = append(args, "origin "+QuoteLiteral(o.Origin))
	}
	return args
}
