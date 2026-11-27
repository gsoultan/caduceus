// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

// Package gss logs caduceus in to a PostgreSQL server with Kerberos: the gss
// and sspi methods of pg_hba.conf. It is a module of its own, so that the
// Kerberos library it is built on, gokrb5, reaches only a program that asks
// for Kerberos; caduceus itself requires nothing.
//
//	config, _ := wire.ParseConfig("host=db.example.com user=alice dbname=app")
//	config.NewGSS = gss.New
//	conn, err := caduceus.ConnectConfig(ctx, config, caduceus.Options{})
//
// New reads the credentials kinit left, from KRB5CCNAME or the default
// cache, and the Kerberos configuration from KRB5_CONFIG or /etc/krb5.conf.
// A service that logs in from a keytab builds a gokrb5 client and passes it
// to FromClient.
//
// The server's principal is postgres/host, krbsrvname/host with krbsrvname
// in the connection string, or krbspn as it is. The server proves itself in
// return: its reply is checked against the session key, and one that does
// not match fails the login.
package gss
