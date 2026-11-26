package config

import (
	"reflect"
	"slices"
	"strings"
)

// field is one configuration field of an options struct. It is the single
// field enumeration behind defaults, the configuration-file key check and
// redaction, so all three agree on keys, squash and secrets.
type field struct {
	key    string       // configuration key: mapstructure tag, or lower-cased field name
	index  []int        // path from the enumerated struct, through squashed structs
	typ    reflect.Type // field type
	secret bool         // tagged secret:"true", itself or on a squashed ancestor
}

// structFields returns the configuration fields of struct type t: exported
// and not tagged mapstructure:"-". The fields of a struct tagged ",squash"
// take its place at t's level and inherit its secret mark.
func structFields(t reflect.Type) []field {
	var out []field
	for i := range t.NumField() {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("mapstructure"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		secret := f.Tag.Get("secret") == "true"
		if slices.Contains(strings.Split(opts, ","), "squash") && f.Type.Kind() == reflect.Struct {
			for _, inner := range structFields(f.Type) {
				inner.index = append([]int{i}, inner.index...)
				inner.secret = inner.secret || secret
				out = append(out, inner)
			}
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		out = append(out, field{key: name, index: []int{i}, typ: f.Type, secret: secret})
	}
	return out
}

// derefType strips pointers from t.
func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// isBlock reports whether t, without pointers, is a struct with
// configuration fields: a block whose fields form dotted keys.
func isBlock(t reflect.Type) bool {
	t = derefType(t)
	return t.Kind() == reflect.Struct && len(structFields(t)) > 0
}
