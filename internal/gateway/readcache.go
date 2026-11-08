package gateway

import (
	"reflect"
	"sync"
)

// readCache keeps the result of a read for as long as nothing wrote to the
// database. The gateway reads the same graph and settings for every prompt of
// every session and for every poll of the TUI, and a write is much rarer than
// those reads. The sqlitecache connector counts the statements that may
// change the database; a cached result is current while that count is the
// same as when it was read.
type readCache struct {
	mu sync.Mutex
	m  map[string]cachedRead
}

type cachedRead struct {
	writes uint64
	second int64
	value  any
}

// readThrough returns the value kept under key if nothing has written since it
// was read, and otherwise calls load and keeps its value. The value is shared:
// callers that may change it must copy it (see copyOf).
//
// A read that depends on the time, such as a window of the last day or a
// decay, passes timed=true: its value is kept for the second it was read in,
// by the store's clock, and not longer.
//
// A value is kept only if no write ran while load did, so it never mixes the
// data from before and after a write.
func (s *Store) readThrough(key string, timed bool, load func() (any, error)) (any, error) {
	if s.connector == nil {
		return load()
	}
	var second int64
	if timed {
		second = s.now().Unix()
	}
	writes := s.connector.Writes()
	s.rc.mu.Lock()
	e, ok := s.rc.m[key]
	s.rc.mu.Unlock()
	if ok && e.writes == writes && e.second == second {
		return e.value, nil
	}
	v, err := load()
	if err != nil {
		return nil, err
	}
	if s.connector.Writes() == writes {
		s.rc.mu.Lock()
		if s.rc.m == nil {
			s.rc.m = map[string]cachedRead{}
		}
		s.rc.m[key] = cachedRead{writes: writes, second: second, value: v}
		s.rc.mu.Unlock()
	}
	return v, nil
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
