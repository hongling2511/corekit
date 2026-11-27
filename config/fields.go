package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// field is one configuration field of an options struct. It is the single
// field enumeration behind defaults, the configuration-file key check and
// redaction, so all three agree on keys, squash and secrets.
type field struct {
	key    string       // lower-cased configuration key: mapstructure tag, or field name
	index  []int        // path from the enumerated struct, through squashed structs
	typ    reflect.Type // field type
	secret bool         // tagged secret:"true", itself or on a squashed ancestor
}

// structFields returns the configuration fields of struct type t: exported
// and not tagged mapstructure:"-". The fields of a struct tagged ",squash"
// take its place at t's level and inherit its secret mark. checkType rejects
// the shapes this enumeration does not model.
func structFields(t reflect.Type) []field {
	var out []field
	for i := range t.NumField() {
		f := t.Field(i)
		name, opts := tagParts(f)
		if !f.IsExported() || name == "-" {
			continue
		}
		secret := f.Tag.Get("secret") == "true"
		if slices.Contains(opts, "squash") && f.Type.Kind() == reflect.Struct {
			for _, inner := range structFields(f.Type) {
				inner.index = append([]int{i}, inner.index...)
				inner.secret = inner.secret || secret
				out = append(out, inner)
			}
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, field{key: strings.ToLower(name), index: []int{i}, typ: f.Type, secret: secret})
	}
	return out
}

func tagParts(f reflect.StructField) (name string, opts []string) {
	name, rest, _ := strings.Cut(f.Tag.Get("mapstructure"), ",")
	return name, strings.Split(rest, ",")
}

// checkType rejects options types whose decoding the field enumeration does
// not model: two fields with one key at the same level (viper fills both),
// ",squash" on anything but a struct, ",remain", and a container type that
// holds itself (type Tree []Tree), which has no struct to stop at.
func checkType(t reflect.Type) error {
	return checkTypeAt(t, "", map[reflect.Type]bool{})
}

func checkTypeAt(t reflect.Type, path string, seen map[reflect.Type]bool) error {
	containers := map[reflect.Type]bool{}
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			if containers[t] {
				return fmt.Errorf("options type %s: key %s: recursive type is not supported", t, strings.TrimSuffix(path, "."))
			}
			containers[t] = true
			t = t.Elem()
			continue
		}
		break
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return nil
	}
	seen[t] = true
	if err := checkTags(t, path); err != nil {
		return err
	}
	keys := map[string]bool{}
	for _, f := range structFields(t) {
		if keys[f.key] {
			return fmt.Errorf("options type %s: two fields use the key %s", t, path+f.key)
		}
		keys[f.key] = true
		if err := checkTypeAt(f.typ, path+f.key+".", seen); err != nil {
			return err
		}
	}
	return nil
}

// checkTags rejects ",remain" and ",squash" on a non-struct in t and in the
// structs it squashes.
func checkTags(t reflect.Type, path string) error {
	for i := range t.NumField() {
		f := t.Field(i)
		name, opts := tagParts(f)
		if !f.IsExported() || name == "-" {
			continue
		}
		switch {
		case slices.Contains(opts, "remain"):
			return fmt.Errorf("options type %s: field %s: \",remain\" is not supported", t, f.Name)
		case slices.Contains(opts, "squash") && f.Type.Kind() != reflect.Struct:
			return fmt.Errorf("options type %s: field %s: \",squash\" is supported only on a struct, not %s", t, f.Name, f.Type)
		case slices.Contains(opts, "squash"):
			if err := checkTags(f.Type, path); err != nil {
				return err
			}
		}
	}
	return nil
}

// isBlock reports whether t is a struct with configuration fields: a block
// whose fields form dotted keys.
func isBlock(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && len(structFields(t)) > 0
}

// isBlockPointer reports whether t is a pointer to a block.
func isBlockPointer(t reflect.Type) bool {
	return t.Kind() == reflect.Pointer && isBlock(t.Elem())
}
