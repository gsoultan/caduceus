// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package stdlib

import (
	"context"
	"database/sql"
	"database/sql/driver"

	"github.com/gsoultan/caduceus"
	"github.com/gsoultan/caduceus/wire"
)

// DriverName is the name the driver is registered under.
const DriverName = "caduceus"

// Driver is the database/sql driver. Its connection string is any that
// wire.ParseConfig reads: a URL or libpq's key=value form.
type Driver struct{}

var _ driver.DriverContext = Driver{}

func init() {
	sql.Register(DriverName, Driver{})
}

// Open opens one connection. database/sql calls OpenConnector instead, which
// parses the connection string once rather than for each connection.
func (d Driver) Open(dsn string) (driver.Conn, error) {
	connector, err := d.OpenConnector(dsn)
	if err != nil {
		return nil, err
	}
	// database/sql gives Open no context; connect_timeout bounds it.
	return connector.Connect(context.Background())
}

// OpenConnector parses the connection string, once for every connection the
// pool will open with it.
func (Driver) OpenConnector(dsn string) (driver.Connector, error) {
	config, err := wire.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return NewConnector(config, caduceus.Options{}), nil
}
