// Copyright (c) 2026 Gembit Soultan Shirazi <gembit.soultan@gmail.com>. All rights reserved.

package caduceus

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/caduceus/wire"
)

type layoutAudit struct {
	CreatedBy string
	Revision  int
}

type layoutUser struct {
	ID       int64
	UserName string `db:"login"`
	Email    *string
	Secret   string `db:"-"`
	internal int
	layoutAudit
	Home layoutAddress `db:"home"`
}

type layoutAddress struct {
	City string
}

type layoutBase struct {
	ID   int64
	Name string
}

// layoutShadow's own ID shadows layoutBase's, as Go promotes it.
type layoutShadow struct {
	layoutBase
	ID int64
}

// layoutShadowFirst declares its own ID before the embedded struct.
type layoutShadowFirst struct {
	ID int64
	layoutBase
}

type layoutPair struct{ A, B int64 }

// layoutTaggedEmbed tags its embedded struct, which is flattened all the same.
type layoutTaggedEmbed struct {
	layoutPair `db:"pair"`
	C          int64
}

// layoutSkippedEmbed skips its embedded struct with db:"-".
type layoutSkippedEmbed struct {
	layoutPair `db:"-"`
	C          int64
}

func columnsOf(names ...string) []wire.Field {
	fields := make([]wire.Field, len(names))
	for i, name := range names {
		fields[i].Name = name
	}
	return fields
}

// TestStructLayout proves how columns are matched to fields, as pgx matches
// them: a db tag is the column's name exactly, case and all; an untagged
// field matches without regard to case or underscores; db:"-" and unexported
// fields take nothing; an embedded struct's fields are its parent's, tagged
// or not, unless it is tagged db:"-"; a struct's own field shadows an
// embedded one of the same name. Every column needs a field, and every field
// a column unless lax; by position, the counts must agree.
//
// The shadowing rows used to fail: the column went to the first field of its
// name, the embedded one, so strict refused the struct's own field as having
// no column, and lax left it zero. The tagged-embedded rows used to fail
// too: a tagged embedded struct was one field, wanting a column of its tag's
// name, where pgx flattens it. And a tag used to match without regard to
// case, so db:"login" took LOGIN, which pgx does not.
func TestStructLayout(t *testing.T) {
	type pair struct{ A, B int }
	tests := []struct {
		name    string
		t       reflect.Type
		how     mapping
		columns []string
		want    [][]int
		err     string
	}{
		{"by name", reflect.TypeFor[layoutUser](), byName,
			[]string{"id", "login", "email", "created_by", "revision", "home"},
			[][]int{{0}, {1}, {2}, {5, 0}, {5, 1}, {6}}, ""},
		{"a tag is matched exactly, case and all", reflect.TypeFor[layoutUser](), byNameLax,
			[]string{"LOGIN"},
			nil, `column "LOGIN" has no field`},
		{"a struct's own field shadows an embedded one", reflect.TypeFor[layoutShadow](), byName,
			[]string{"id", "name"},
			[][]int{{1}, {0, 1}}, ""},
		{"a struct's own field shadows an embedded one, lax", reflect.TypeFor[layoutShadow](), byNameLax,
			[]string{"id"},
			[][]int{{1}}, ""},
		{"a struct's own field shadows an embedded one declared after it", reflect.TypeFor[layoutShadowFirst](), byName,
			[]string{"name", "id"},
			[][]int{{1, 1}, {0}}, ""},
		{"a shadowed field is still wanted when its name is not a column", reflect.TypeFor[layoutShadow](), byName,
			[]string{"id"},
			nil, "field Name of caduceus.layoutShadow has no column"},
		{"a tagged embedded struct is flattened, by name", reflect.TypeFor[layoutTaggedEmbed](), byName,
			[]string{"a", "b", "c"},
			[][]int{{0, 0}, {0, 1}, {1}}, ""},
		{"a tagged embedded struct is flattened, by position", reflect.TypeFor[layoutTaggedEmbed](), byPos,
			[]string{"x", "y", "z"},
			[][]int{{0, 0}, {0, 1}, {1}}, ""},
		{"an embedded struct tagged db:\"-\" is skipped", reflect.TypeFor[layoutSkippedEmbed](), byPos,
			[]string{"c"},
			[][]int{{1}}, ""},
		{"by name in another order", reflect.TypeFor[layoutUser](), byName,
			[]string{"home", "revision", "Created_By", "email", "login", "I_D"},
			[][]int{{6}, {5, 1}, {5, 0}, {2}, {1}, {0}}, ""},
		{"a tag is matched as written, so its field's Go name is not", reflect.TypeFor[layoutUser](), byName,
			[]string{"id", "user_name", "email", "created_by", "revision", "home"},
			nil, `column "user_name" has no field`},
		{"a column without a field", reflect.TypeFor[layoutUser](), byName,
			[]string{"id", "login", "email", "created_by", "revision", "home", "secret"},
			nil, `column "secret" has no field`},
		{"a field without a column", reflect.TypeFor[layoutUser](), byName,
			[]string{"id", "login", "created_by", "revision", "home"},
			nil, "field Email of caduceus.layoutUser has no column"},
		{"lax leaves a field without a column", reflect.TypeFor[layoutUser](), byNameLax,
			[]string{"login", "revision"},
			[][]int{{1}, {5, 1}}, ""},
		{"lax still wants a field for every column", reflect.TypeFor[layoutUser](), byNameLax,
			[]string{"login", "nope"},
			nil, `column "nope" has no field`},
		{"two columns for one field", reflect.TypeFor[layoutUser](), byNameLax,
			[]string{"id", "I_D"},
			nil, `columns "id" and "I_D" both map to field ID`},
		{"by position", reflect.TypeFor[layoutUser](), byPos,
			[]string{"a", "b", "c", "d", "e", "f"},
			[][]int{{0}, {1}, {2}, {5, 0}, {5, 1}, {6}}, ""},
		{"by position, too few columns", reflect.TypeFor[pair](), byPos,
			[]string{"a"}, nil, "1 columns in the result, 2 fields"},
		{"by position, too many columns", reflect.TypeFor[pair](), byPos,
			[]string{"a", "b", "c"}, nil, "3 columns in the result, 2 fields"},
		{"not a struct", reflect.TypeFor[int](), byName,
			[]string{"a"}, nil, "int is not a struct"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout := newStructLayout(tt.t, tt.how, columnsOf(tt.columns...))
			if tt.err != "" {
				if layout.err == nil || !strings.Contains(layout.err.Error(), tt.err) {
					t.Fatalf("err = %v, want one containing %q", layout.err, tt.err)
				}
				return
			}
			if layout.err != nil {
				t.Fatalf("err = %v", layout.err)
			}
			if !reflect.DeepEqual(layout.fields, tt.want) {
				t.Fatalf("fields = %v, want %v", layout.fields, tt.want)
			}
		})
	}
}

// TestStructFieldIndexesDoNotShareBacking guards the index of each field of an
// embedded struct: they are built by appending to the parent's index, and
// once that index has room to spare, as it does three levels down, appending
// to it in place left every field with the last one's index.
func TestStructFieldIndexesDoNotShareBacking(t *testing.T) {
	type c struct{ A, B, C int }
	type b struct{ c }
	type a struct{ b }
	type outer struct {
		X int
		a
	}
	fields := structFields(reflect.TypeFor[outer](), nil, nil)
	var got [][]int
	for _, f := range fields {
		got = append(got, f.index)
	}
	want := [][]int{{0}, {1, 0, 0, 0}, {1, 0, 0, 1}, {1, 0, 0, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("indexes = %v, want %v", got, want)
	}
}

// TestLayoutCacheReusesAndTellsColumnsApart proves a layout is derived once
// for a type and set of columns, and that one derived for other columns, or
// another mapping, is never handed out in its place.
func TestLayoutCacheReusesAndTellsColumnsApart(t *testing.T) {
	cache := newLayoutCache()
	typ := reflect.TypeFor[layoutUser]()
	first := cache.layout(typ, byNameLax, columnsOf("id", "login"))
	if again := cache.layout(typ, byNameLax, columnsOf("id", "login")); again != first {
		t.Fatal("the same type and columns derived the layout again")
	}
	if other := cache.layout(typ, byNameLax, columnsOf("login", "id")); other == first || !reflect.DeepEqual(other.fields, [][]int{{1}, {0}}) {
		t.Fatalf("other columns got %v", other.fields)
	}
	if strict := cache.layout(typ, byName, columnsOf("id", "login")); strict == first || strict.err == nil {
		t.Fatal("another mapping was handed the lax layout")
	}
}

// TestLayoutCacheTellsACollisionFromAHit plants a layout for other columns
// under a key, as a hash collision would, and proves it is not used.
func TestLayoutCacheTellsACollisionFromAHit(t *testing.T) {
	cache := newLayoutCache()
	typ := reflect.TypeFor[layoutUser]()
	columns := columnsOf("login")
	cache.layouts.Store(cache.key(typ, byNameLax, columns), newStructLayout(typ, byNameLax, columnsOf("id")))
	if got := cache.layout(typ, byNameLax, columns); !reflect.DeepEqual(got.fields, [][]int{{1}}) {
		t.Fatalf("fields = %v, want the login field", got.fields)
	}
}

// idColumn returns a column name the untagged ID field takes, made distinct
// by underscores, which the match ignores, so each one is a new set of
// columns that maps.
func idColumn(underscores int) string {
	return "i" + strings.Repeat("_", underscores) + "d"
}

// layoutCount counts the layouts the cache holds.
func layoutCount(cache *layoutCache) int {
	n := 0
	cache.layouts.Range(func(any, any) bool { n++; return true })
	return n
}

// TestLayoutCacheIsBounded hands the cache more sets of column names that map
// than it keeps, as a program querying adversarial column names would, and
// proves it grows past neither its count nor its byte bound.
func TestLayoutCacheIsBounded(t *testing.T) {
	type idOnly struct{ ID int64 }
	typ := reflect.TypeFor[idOnly]()
	tests := []struct {
		name  string
		width int
	}{
		{"many short names", 0},
		{"long names", 8 << 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newLayoutCache()
			for i := range 3 * maxLayouts {
				_ = cache.layout(typ, byNameLax, columnsOf(idColumn(tt.width+i)))
			}
			n := layoutCount(cache)
			if n == 0 || n > maxLayouts || cache.len() > maxLayouts || cache.retained() > maxLayoutBytes {
				t.Fatalf("%d layouts kept, size %d, %d bytes; bounds %d, %d", n, cache.len(), cache.retained(), maxLayouts, maxLayoutBytes)
			}
		})
	}
}

// TestLayoutCacheKeepsOnlyWhatMaps guards the memory of the cache against
// column sets that never map. It used to keep every layout it derived, a
// failed one too, each with a copy of all its column names, so 1024 results
// of 1600 long columns that a struct refused held some 100 MB after the
// connection was gone. Now a layout that does not map is not kept, and one
// by position keeps no names, since only their count matters.
func TestLayoutCacheKeepsOnlyWhatMaps(t *testing.T) {
	typ := reflect.TypeFor[layoutPair]()
	wide := make([]string, 1600)
	for i := range wide {
		wide[i] = strings.Repeat("x", 60) + strconv.Itoa(i)
	}
	tests := []struct {
		name    string
		how     mapping
		columns []string
		kept    int
		bytes   int64
	}{
		{"by name, a column without a field", byNameLax, wide, 0, 0},
		{"by name, a field without a column", byName, []string{"a"}, 0, 0},
		{"by position, a count that does not agree", byPos, wide, 0, 0},
		{"by position, it maps and keeps no names", byPos, []string{strings.Repeat("y", 1000), "z"}, 1, 0},
		{"by name, it maps and keeps its names", byName, []string{"a", "b"}, 1, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newLayoutCache()
			_ = cache.layout(typ, tt.how, columnsOf(tt.columns...))
			if n := layoutCount(cache); n != tt.kept || cache.retained() != tt.bytes {
				t.Fatalf("%d layouts, %d bytes kept; want %d, %d", n, cache.retained(), tt.kept, tt.bytes)
			}
		})
	}
}

// TestLayoutCachePositionalIgnoresNames proves a layout by position is found
// again for other names of the same count, as it does not depend on them.
func TestLayoutCachePositionalIgnoresNames(t *testing.T) {
	cache := newLayoutCache()
	typ := reflect.TypeFor[layoutPair]()
	first := cache.layout(typ, byPos, columnsOf("a", "b"))
	if again := cache.layout(typ, byPos, columnsOf("c", "d")); again != first {
		t.Fatal("other names of the same count derived the positional layout again")
	}
}

// TestLayoutCacheConcurrent runs lookups and stores from many goroutines, for
// the race detector.
func TestLayoutCacheConcurrent(t *testing.T) {
	cache := newLayoutCache()
	typ := reflect.TypeFor[layoutUser]()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 500 {
				layout := cache.layout(typ, byNameLax, columnsOf("login", idColumn((g*500+i)%1500)))
				if layout.err != nil || !reflect.DeepEqual(layout.fields, [][]int{{1}, {0}}) {
					t.Errorf("layout = %v, %v", layout.fields, layout.err)
				}
			}
		})
	}
	wg.Wait()
}

// TestCollectRefusesNil proves the collecting functions return an error,
// rather than panic, for the nil rows Query returns beside an error, and for a
// nil RowToFunc.
func TestCollectRefusesNil(t *testing.T) {
	tests := []struct {
		name string
		call func() error
	}{
		{"CollectRows", func() error { _, err := CollectRows(nil, RowTo[int]); return err }},
		{"AppendRows", func() error { _, err := AppendRows([]int{1}, nil, RowTo[int]); return err }},
		{"CollectOneRow", func() error { _, err := CollectOneRow(nil, RowTo[int]); return err }},
		{"CollectExactlyOneRow", func() error { _, err := CollectExactlyOneRow(nil, RowTo[int]); return err }},
		{"ForEachRow", func() error { _, err := ForEachRow(nil, nil, nil); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, errNilRows) {
				t.Fatalf("err = %v, want errNilRows", err)
			}
		})
	}
}
