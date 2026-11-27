// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib

import (
	"context"
	"database/sql"
	"database/sql/driver"

	"github.com/gsoultan/caduceus"
	"github.com/gsoultan/caduceus/wire"
)

// Connector opens connections from one wire.Config, which it reads nothing
// again to do: no environment, no password file.
type Connector struct {
	config  *wire.Config
	options caduceus.Options
}

var _ driver.Connector = (*Connector)(nil)

// NewConnector returns a Connector for config, which it copies, so that a
// change to config afterwards does not reach the connections it opens.
func NewConnector(config *wire.Config, options caduceus.Options) *Connector {
	return &Connector{config: config.Copy(), options: options}
}

// OpenDB is sql.OpenDB with a Connector for config.
func OpenDB(config *wire.Config, options caduceus.Options) *sql.DB {
	return sql.OpenDB(NewConnector(config, options))
}

// Connect opens a connection, bounded by ctx and the config's
// ConnectTimeout.
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := caduceus.ConnectConfig(ctx, c.config, c.options)
	if err != nil {
		return nil, err
	}
	return &Conn{conn: conn}, nil
}

// Driver returns the driver, as database/sql asks of a Connector.
func (*Connector) Driver() driver.Driver { return Driver{} }
