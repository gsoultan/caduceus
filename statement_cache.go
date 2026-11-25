// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

// statementCache holds a connection's statements by SQL, evicting the least
// recently used past its capacity. They are prepared statements kept on the
// server, or, for a connection that keeps none there, what the server said of
// statements that are parsed every time they run.
//
// The map is allocated small and grows with use, unlike pgx's caches, which
// preallocated their full capacity on every connection: that was 57% of the
// pool's heap before a statement had been prepared.
type statementCache struct {
	capacity int
	bySQL    map[string]*statement
	// head is the most recently used statement; head.prev is the least.
	head *statement
}

func newStatementCache(capacity int) *statementCache {
	return &statementCache{capacity: capacity, bySQL: make(map[string]*statement, min(capacity, 8))}
}

// get returns the statement for sql and marks it used.
func (c *statementCache) get(sql string) *statement {
	s := c.bySQL[sql]
	if s != nil && s != c.head {
		c.unlink(s)
		c.push(s)
	}
	return s
}

// put adds a statement and returns the one it evicted, if any, so its server
// side can be closed. A cache with no capacity keeps nothing.
//
// A statement for a text the cache already holds takes the place of the one
// it holds, which is the one evicted. Pushed in beside it instead, the old one
// stayed in the recency list and out of the map, and once it was the oldest
// every eviction picked it and removed nothing: the map grew without bound.
func (c *statementCache) put(s *statement) *statement {
	if c.capacity <= 0 {
		return nil
	}
	if held := c.bySQL[s.sql]; held != nil {
		return c.replace(held, s)
	}
	c.bySQL[s.sql] = s
	c.push(s)
	if len(c.bySQL) <= c.capacity {
		return nil
	}
	oldest := c.head.prev
	c.remove(oldest)
	return oldest
}

// replace puts s where held is, and returns held, unless the two are one.
func (c *statementCache) replace(held, s *statement) *statement {
	c.unlink(held)
	c.bySQL[s.sql] = s
	c.push(s)
	if held == s {
		return nil
	}
	return held
}

// remove forgets a statement.
func (c *statementCache) remove(s *statement) {
	if c.bySQL[s.sql] != s {
		return
	}
	delete(c.bySQL, s.sql)
	c.unlink(s)
}

// clear forgets every statement. A pool that resets its connections calls it
// on every release, so it allocates nothing.
func (c *statementCache) clear() {
	clear(c.bySQL)
	c.head = nil
}

func (c *statementCache) push(s *statement) {
	if c.head == nil {
		s.prev, s.next = s, s
		c.head = s
		return
	}
	tail := c.head.prev
	s.prev, s.next = tail, c.head
	tail.next, c.head.prev = s, s
	c.head = s
}

func (c *statementCache) unlink(s *statement) {
	if s.next == s {
		c.head = nil
	} else {
		s.prev.next, s.next.prev = s.next, s.prev
		if c.head == s {
			c.head = s.next
		}
	}
	s.prev, s.next = nil, nil
}
