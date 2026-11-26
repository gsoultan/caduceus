// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/gsoultan/caduceus/wire"
)

// maxStatementName is the longest name the server keeps whole: NAMEDATALEN
// less its terminator. A longer one is cut there, so two that share their
// first 63 bytes would name one statement.
const maxStatementName = 63

// generatedPrefix begins every name the connection gives a statement itself.
// A caller's name that begins with it is never sent as it is, so no Prepare
// can take, or Deallocate close, a statement the cache holds.
const generatedPrefix = "caduceus_"

// Prepare parses sql on the server as a prepared statement and returns what
// the server said of it. Exec, Query, QueryRow, QueryInto, SendBatch and
// SendPipeline then run it when given name in the place of SQL text, with a
// Bind and an Execute and no Parse. An empty name names the statement by sql
// itself, so the same text run afterwards runs the prepared statement.
//
// Preparing a name again with the same text returns what is held, without a
// round trip, unless the server has since said that the statement no longer
// holds, as it does with 0A000 for a table altered under it; it is then
// closed and parsed afresh, in one round trip. With another text, as in pgx,
// the Parse goes to the server, which refuses it with 42P05, prepared
// statement already exists: the statement held under name stays prepared and
// runs by that name as before. To give a name another text, Deallocate it
// first.
//
// A prepared statement lives outside the statement cache, which neither
// evicts it nor sees it, and it is kept on the server whatever the options,
// until Deallocate, DeallocateAll, ForgetStatements or the end of the session.
// Nothing bounds how many a connection holds: each costs memory here and in
// the server backend, for every connection that prepares it. SQL built at run
// time belongs in the statement cache (see Options), which is capped, not in
// Prepare(ctx, "", sql), unless each such Prepare is paired with a Deallocate.
// Parse is not transactional: a statement prepared inside a transaction that
// is rolled back stays prepared.
//
// A pooler in transaction mode gives each transaction whatever server
// connection is free, and a statement prepared in one is not on the next.
// Behind such a pooler, prepare and run a statement inside one transaction,
// or not at all.
//
// A Parse the server refuses returns its *wire.Error and prepares nothing; a
// statement already held under name is left as it was, unless it was stale.
func (s *Conn) Prepare(ctx context.Context, name, sql string) (*StatementDescription, error) {
	if name == "" {
		name = sql
	}
	held := s.prepared[name]
	if current(held, sql) {
		return describe(held), nil
	}
	if err := s.begin(ctx); err != nil {
		return nil, err
	}
	s.releaseStale(name, held)
	stmt, err := s.parse(serverName(name, sql), sql)
	if err = s.finish(err); err != nil {
		return nil, err
	}
	stmt.prepared = true
	s.keep(name, stmt)
	return describe(stmt), nil
}

// Deallocate closes the statement prepared as name, on the server and here.
// A name that is not prepared is closed all the same, which the server
// accepts, so Deallocate is idempotent; it never closes a statement of the
// cache.
func (s *Conn) Deallocate(ctx context.Context, name string) error {
	server := serverName(name, "")
	if held := s.prepared[name]; held != nil {
		server = held.name
	}
	if err := s.begin(ctx); err != nil {
		return err
	}
	buf := wire.AppendClose(s.closeEvicted(s.conn.Buffer()), 'S', server)
	err := s.conn.Send(wire.AppendSync(buf))
	if err == nil {
		_, err = s.drain(nil)
	}
	if err = s.finish(err); err != nil {
		return err
	}
	delete(s.prepared, name)
	return nil
}

// DeallocateAll runs DEALLOCATE ALL, which closes every prepared statement of
// the session, and forgets them here: those prepared with Prepare and those of
// the statement cache, and, on a connection that keeps none on the server,
// what the cache remembers of statements it parses every time.
func (s *Conn) DeallocateAll(ctx context.Context) error {
	if err := s.begin(ctx); err != nil {
		return err
	}
	_, err := s.simpleBehindBegin("DEALLOCATE ALL")
	if err = s.finish(err); err != nil {
		return err
	}
	s.forgetStatements()
	return nil
}

// current reports whether held is a statement prepared with sql that the
// server has not said is out of date.
func current(held *statement, sql string) bool {
	return held != nil && held.sql == sql && !held.stale
}

// releaseStale drops the statement held as name if the server has said it no
// longer holds, and queues it to be closed ahead of the next Parse. One that
// still holds is kept, so that a Parse of another text under its name is
// refused by the server, as in pgx, rather than replacing it.
func (s *Conn) releaseStale(name string, held *statement) {
	if held == nil || !held.stale {
		return
	}
	delete(s.prepared, name)
	s.closing = append(s.closing, held.name)
}

// keep holds a statement the caller prepared as name.
func (s *Conn) keep(name string, stmt *statement) {
	if s.prepared == nil {
		s.prepared = make(map[string]*statement)
	}
	s.prepared[name] = stmt
}

// known returns the statement sql stands for: one the caller prepared by
// that name, or the one the cache holds for that text.
func (s *Conn) known(sql string) *statement {
	if stmt := s.prepared[sql]; stmt != nil {
		return stmt
	}
	return s.cache.get(sql)
}

// serverName is the name a statement the caller prepares as name, from sql,
// goes by on the server: the name itself, unless it is the text, would be cut
// or garbled by the server, or could be taken for one the cache generated.
func serverName(name, sql string) string {
	if name != sql && plainName(name) {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	return generatedPrefix + "p_" + hex.EncodeToString(digest[:20])
}

// plainName reports whether name reaches the server and comes back as it is,
// and is no name the cache could generate.
func plainName(name string) bool {
	return name != "" && len(name) <= maxStatementName &&
		!strings.ContainsRune(name, 0) && !strings.HasPrefix(name, generatedPrefix)
}
