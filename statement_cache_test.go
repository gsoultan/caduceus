// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import "testing"

// The cache gives up the statement least recently used, and tells its caller
// which that was, since one kept on the server has to be closed there.
func TestStatementCacheEvictsTheLeastRecentlyUsed(t *testing.T) {
	cache := newStatementCache(2)
	a, b, c := &statement{sql: "a"}, &statement{sql: "b"}, &statement{sql: "c"}
	if evicted := cache.put(a); evicted != nil {
		t.Fatalf("put(a) evicted %q", evicted.sql)
	}
	if evicted := cache.put(b); evicted != nil {
		t.Fatalf("put(b) evicted %q", evicted.sql)
	}
	if cache.get("a") != a {
		t.Fatal("get(a) did not find a")
	}
	if evicted := cache.put(c); evicted != b {
		t.Fatalf("put(c) evicted %v, want b, which was used before a", evicted)
	}
	if cache.get("b") != nil || cache.get("a") != a || cache.get("c") != c {
		t.Fatal("the cache does not hold a and c alone")
	}
	cache.remove(a)
	cache.remove(a)
	if cache.get("a") != nil || cache.get("c") != c {
		t.Fatal("removing a, twice, did not leave c alone")
	}
	// A statement that is no longer the one cached for its text, as one read
	// with after the server described it afresh is not, leaves the one that
	// is.
	cache.remove(&statement{sql: "c"})
	if cache.get("c") != c {
		t.Fatal("removing another statement of the same text removed the cached one")
	}
	cache.clear()
	if cache.get("c") != nil {
		t.Fatal("clear() left a statement")
	}
	// What was cleared is not linked to what comes after.
	if evicted := cache.put(a); evicted != nil {
		t.Fatalf("put(a) into a cleared cache evicted %q", evicted.sql)
	}
	if evicted := cache.put(b); evicted != nil {
		t.Fatalf("put(b) into a cleared cache evicted %q", evicted.sql)
	}
	if evicted := cache.put(&statement{sql: "d"}); evicted != a {
		t.Fatalf("a third statement evicted %v, want a", evicted)
	}
}

// A cache with no room remembers nothing: a connection set to remember no
// descriptions asks the server every time.
func TestStatementCacheWithNoCapacityKeepsNothing(t *testing.T) {
	cache := newStatementCache(0)
	if evicted := cache.put(&statement{sql: "a"}); evicted != nil {
		t.Fatalf("put evicted %q from a cache that holds nothing", evicted.sql)
	}
	if cache.get("a") != nil {
		t.Fatal("a cache with no capacity kept a statement")
	}
}

// A statement for a text the cache already holds takes the other's place. It
// used to be pushed in beside it: the one it replaced stayed in the recency
// list with nothing in the map, every eviction from then on picked it and
// removed nothing, and the map grew past its capacity for good.
func TestStatementCacheReplacesAStatementOfTheSameText(t *testing.T) {
	cache := newStatementCache(2)
	first, second := &statement{sql: "a"}, &statement{sql: "a"}
	cache.put(first)
	if evicted := cache.put(second); evicted != first {
		t.Fatalf("a second statement for the text evicted %v, want the first", evicted)
	}
	if cache.get("a") != second {
		t.Fatal("the cache does not hold the second statement for the text")
	}
	if evicted := cache.put(second); evicted != nil {
		t.Fatalf("putting the statement the cache holds evicted %q", evicted.sql)
	}
	for _, text := range []string{"b", "c", "d", "e", "f"} {
		cache.put(&statement{sql: text})
		if len(cache.bySQL) > 2 {
			t.Fatalf("after %q the cache holds %d statements, more than its capacity of 2", text, len(cache.bySQL))
		}
	}
	if cache.get("e") == nil || cache.get("f") == nil {
		t.Fatal("the cache does not hold the two statements put last")
	}
}
