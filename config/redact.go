package config

import (
	"fmt"
	"reflect"
)

// Redacted returns the configuration held by opts (a pointer to a struct)
// as dotted keys and values, for printing; it is empty for any other value.
//
// Values are rebuilt from their fields, never from a type's own formatting
// (MarshalText, MarshalJSON, String), so a secret cannot leak through it:
//   - a non-empty field tagged `secret:"true"` is "******" as a whole,
//     whatever its type;
//   - nested structs, also behind pointers, flatten into dotted keys; structs
//     inside slices, arrays, maps and interfaces become nested maps with
//     their own secrets hidden;
//   - basic values print as their underlying bool, number or string;
//   - anything that cannot be shown safely is "******": a struct without
//     exported fields, a map whose keys are not basic values, functions,
//     channels, unsafe pointers and complex numbers;
//   - a value that refers back to itself on the current path is "<cycle>";
//     a value shared by several fields prints at each of them.
func Redacted(opts any) map[string]any {
	out := map[string]any{}
	if v := reflect.Indirect(reflect.ValueOf(opts)); v.Kind() == reflect.Struct {
		r := redactor{onPath: map[visit]bool{}}
		r.flatten(v, "", out)
	}
	return out
}

const (
	redacted = "******"
	cycle    = "<cycle>"
)

// redactor tracks the pointers, maps and slices on the current path.
type redactor struct{ onPath map[visit]bool }

// visit identifies a reference value; a slice also by its length, so a
// shorter view of the same storage is not taken for a cycle.
type visit struct {
	ptr uintptr
	typ reflect.Type
	len int
}

// enter marks a non-nil pointer, map or slice as on the path; it reports
// false for a cycle. leave must be called when enter returns true.
func (r redactor) enter(v reflect.Value) (visit, bool) {
	key := visit{ptr: v.Pointer(), typ: v.Type()}
	if v.Kind() == reflect.Slice {
		key.len = v.Len()
	}
	if r.onPath[key] {
		return key, false
	}
	r.onPath[key] = true
	return key, true
}

func (r redactor) leave(key visit) { delete(r.onPath, key) }

// flatten writes the redacted fields of struct v into out under dotted keys;
// blocks, also behind pointers, flatten further.
func (r redactor) flatten(v reflect.Value, prefix string, out map[string]any) {
	for _, f := range structFields(v.Type()) {
		key := prefix + f.key
		fv := v.FieldByIndex(f.index)
		switch {
		case f.secret && !fv.IsZero():
			out[key] = redacted
		case fv.Kind() == reflect.Struct && isBlock(f.typ):
			r.flatten(fv, key+".", out)
		case fv.Kind() == reflect.Pointer && !fv.IsNil() && isBlock(f.typ):
			mark, ok := r.enter(fv)
			if !ok {
				out[key] = cycle
				continue
			}
			r.flatten(fv.Elem(), key+".", out)
			r.leave(mark)
		default:
			out[key] = r.project(fv)
		}
	}
}

// project returns a printable copy of v with secrets hidden.
func (r redactor) project(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Invalid:
		return nil
	case reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return r.project(v.Elem())
	case reflect.Pointer, reflect.Map, reflect.Slice:
		if v.IsNil() {
			return nil
		}
		mark, ok := r.enter(v)
		if !ok {
			return cycle
		}
		defer r.leave(mark)
		switch v.Kind() {
		case reflect.Pointer:
			return r.project(v.Elem())
		case reflect.Map:
			return r.projectMap(v)
		default:
			return r.projectList(v)
		}
	case reflect.Array:
		return r.projectList(v)
	case reflect.Struct:
		if !isBlock(v.Type()) {
			return redacted
		}
		m := map[string]any{}
		for _, f := range structFields(v.Type()) {
			fv := v.FieldByIndex(f.index)
			if f.secret && !fv.IsZero() {
				m[f.key] = redacted
				continue
			}
			m[f.key] = r.project(fv)
		}
		return m
	default:
		if b, ok := basic(v); ok {
			return b
		}
		return redacted
	}
}

// projectMap projects a map; one whose keys are not basic values is hidden.
func (r redactor) projectMap(v reflect.Value) any {
	m := make(map[string]any, v.Len())
	for it := v.MapRange(); it.Next(); {
		k, ok := basic(it.Key())
		if !ok {
			return redacted
		}
		m[fmt.Sprint(k)] = r.project(it.Value())
	}
	return m
}

func (r redactor) projectList(v reflect.Value) []any {
	list := make([]any, v.Len())
	for i := range v.Len() {
		list[i] = r.project(v.Index(i))
	}
	return list
}

// basic returns v as its underlying bool, int64, uint64, float64 or string,
// bypassing any methods of its named type.
func basic(v reflect.Value) (any, bool) {
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint(), true
	case reflect.Float32, reflect.Float64:
		return v.Float(), true
	case reflect.String:
		return v.String(), true
	}
	return nil, false
}
