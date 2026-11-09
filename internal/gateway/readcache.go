package gateway

import (
	"reflect"
	"sort"
	"strings"
	"sync"
)

// readCache keeps the result of a read for as long as the tables it read have
// not been written. The gateway reads the same graph and settings for every
// prompt of every session and for every poll of the TUI, and a write to one of
// those tables is much rarer than the reads. The sqlitecache connector counts
// the statements that change each table (a trigger's writes included); a
// cached result is current while the count of every table it read is the same
// as when it was read. A write to another table does not touch it.
type readCache struct {
	mu sync.Mutex
	m  map[string]cachedRead
}

type cachedRead struct {
	stamp  uint64
	second int64
	value  any
}

// tableUnion joins lists of table names into one sorted list without
// repeats. Names are lower case.
func tableUnion(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, t := range l {
			t = strings.ToLower(t)
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	sort.Strings(out)
	return out
}

// readThrough returns the value kept under key if none of the tables has been
// written since it was read, and otherwise calls load and keeps its value. The
// value is shared: callers that may change it must copy it (see copyOf).
//
// tables is every table that load reads, through any function it calls. A
// table that is missing from it is a stale read: a write to that table leaves
// the kept value in place. Declare the set next to the queries (see
// graphTables) and keep it true; TestCachedReadsDeclareEveryTableTheyRead
// fails when a query reads a table the set does not name. An empty set means
// any write to the database.
//
// A key may name one column of a table, as table.column. The value then
// depends on that column and on which rows exist, and a write that leaves the
// column's value as it was, or that changes other columns, keeps the value.
// The graph reads four columns of sessions this way, so a session.upsert that
// rewrites its status keeps the graph (see sqlitecache.Stamp). Name every column
// the load reads from a table, and no other: the guard test checks it.
//
// A read that depends on the time, such as a window of the last day or a
// decay, passes timed=true: its value is kept for the second it was read in,
// by the store's clock, and not longer.
//
// A value is kept only if no write to its tables ran while load did, so it
// never mixes the data from before and after a write.
func (s *Store) readThrough(key string, tables []string, timed bool, load func() (any, error)) (any, error) {
	if s.connector == nil {
		return load()
	}
	var second int64
	if timed {
		second = s.now().Unix()
	}
	stamp := s.readStamp(tables)
	s.rc.mu.Lock()
	e, ok := s.rc.m[key]
	s.rc.mu.Unlock()
	if ok && e.stamp == stamp && e.second == second {
		return e.value, nil
	}
	v, err := load()
	if err != nil {
		return nil, err
	}
	if s.readStamp(tables) == stamp {
		s.rc.mu.Lock()
		if s.rc.m == nil {
			s.rc.m = map[string]cachedRead{}
		}
		s.rc.m[key] = cachedRead{stamp: stamp, second: second, value: v}
		s.rc.mu.Unlock()
	}
	return v, nil
}

// readStamp is the number that tells whether the tables were written.
func (s *Store) readStamp(tables []string) uint64 {
	if len(tables) == 0 {
		return s.connector.Writes()
	}
	return s.connector.Stamp(tables...)
}

// copyOf returns a copy of v that shares no slice, map or pointer with it.
// Unexported fields are copied as they are, so it is for plain data.
func copyOf[T any](v T) T {
	src := reflect.ValueOf(&v).Elem()
	deepCopyValue(src)
	return v
}

// deepCopyValue replaces the references inside v with copies, in place.
func deepCopyValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			n := reflect.New(v.Type().Elem())
			n.Elem().Set(v.Elem())
			deepCopyValue(n.Elem())
			v.Set(n)
		}
	case reflect.Slice:
		if !v.IsNil() {
			n := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			reflect.Copy(n, v)
			for i := 0; i < n.Len(); i++ {
				deepCopyValue(n.Index(i))
			}
			v.Set(n)
		}
	case reflect.Map:
		if !v.IsNil() {
			n := reflect.MakeMapWithSize(v.Type(), v.Len())
			for it := v.MapRange(); it.Next(); {
				e := reflect.New(v.Type().Elem()).Elem()
				e.Set(it.Value())
				deepCopyValue(e)
				n.SetMapIndex(it.Key(), e)
			}
			v.Set(n)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanSet() {
				deepCopyValue(f)
			}
		}
	case reflect.Interface:
		if !v.IsNil() {
			e := reflect.New(v.Elem().Type()).Elem()
			e.Set(v.Elem())
			deepCopyValue(e)
			v.Set(e)
		}
	}
}
