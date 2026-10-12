package changeset

import (
	"bytes"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"unicode/utf8"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/project"
)

// ProjectHash returns the versioned canonical whole-state hash used for
// optimistic validation. It rejects structurally invalid/non-JSON project state.
// Extension values use project persistence semantics, including float64 numbers
// after decoding JSON; typed project fields retain their declared precision.
func ProjectHash(p domain.Project) (string, error) {
	_, hash, err := snapshotProject(p)
	return hash, err
}

func snapshotProject(p domain.Project) (domain.Project, string, error) {
	// Clone the aggregate explicitly so extension values retain their numeric
	// types and large integers never round through float64.
	p.CreatedAt = p.CreatedAt.UTC().Round(0)
	p.UpdatedAt = p.UpdatedAt.UTC().Round(0)
	if p.Plan != nil {
		plan := *p.Plan
		p.Plan = &plan
		plan.CreatedAt = plan.CreatedAt.UTC().Round(0)
		plan.Shots = append([]domain.Shot(nil), plan.Shots...)
		for i := range plan.Shots {
			s := &plan.Shots[i]
			s.DependsOn = append([]domain.ID(nil), s.DependsOn...)
			s.Primitives = append([]domain.Primitive(nil), s.Primitives...)
			for j := range s.Primitives {
				m, err := copyMap(s.Primitives[j].Properties)
				if err != nil {
					return domain.Project{}, "", err
				}
				s.Primitives[j].Properties = m
			}
		}
	}
	if p.Timeline != nil {
		tl := *p.Timeline
		p.Timeline = &tl
		tl.Entries = append([]domain.TimelineEntry(nil), tl.Entries...)
		tl.Elements = append([]domain.VisualElement(nil), tl.Elements...)
		for i := range tl.Elements {
			e := &tl.Elements[i]
			if e.Bounds != nil {
				b := *e.Bounds
				e.Bounds = &b
			}
			m, err := copyMap(e.EditableProperties)
			if err != nil {
				return domain.Project{}, "", err
			}
			e.EditableProperties = m
		}
	}
	p.Assets = append([]domain.Asset(nil), p.Assets...)
	for i := range p.Assets {
		m, err := copyMap(p.Assets[i].Metadata)
		if err != nil {
			return domain.Project{}, "", err
		}
		p.Assets[i].Metadata = m
	}
	// Validate UTF-8 before JSON can replace invalid bytes and create hash aliases.
	if !validStrings(reflect.ValueOf(p)) || project.Validate(p) != nil {
		return domain.Project{}, "", ErrInvalid
	}
	b, err := json.Marshal(p)
	if err != nil || len(b) > MaxProjectBytes {
		return domain.Project{}, "", ErrInvalid
	}
	// Hash the persisted representation: project.Unmarshal decodes extension
	// numbers as float64, even when callers supplied large integers. Normalize
	// through the same typed JSON decode so save/load cannot invalidate proposals.
	// Keep p as the separately owned, type-preserving in-memory snapshot.
	var persisted domain.Project
	if json.Unmarshal(b, &persisted) != nil {
		return domain.Project{}, "", ErrInvalid
	}
	b, err = json.Marshal(persisted)
	if err != nil || len(b) > MaxProjectBytes {
		return domain.Project{}, "", ErrInvalid
	}
	// Sort every object's keys. UseNumber preserves exact typed integer fields
	// such as durations; only extension numbers follow float64 persistence rules.
	var tree any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if decoder.Decode(&tree) != nil {
		return domain.Project{}, "", ErrInvalid
	}
	b, err = json.Marshal(tree)
	if err != nil {
		return domain.Project{}, "", ErrInvalid
	}
	h := sha256.New()
	h.Write([]byte("changeset-project-v1\x00"))
	h.Write(b)
	return p, hex.EncodeToString(h.Sum(nil)), nil
}

// Extension values are JSON-compatible scalar/maps/slices/arrays plus Bounds.
// Arbitrary objects, custom marshalers and pointer graphs are not project data.
func copyMap(in map[string]any) (map[string]any, error) {
	if in == nil {
		return nil, nil
	}
	v, err := copyValue(reflect.ValueOf(in), 0)
	if err != nil {
		return nil, err
	}
	return v.Interface().(map[string]any), nil
}

func copyValue(v reflect.Value, depth int) (reflect.Value, error) {
	if depth > 64 {
		return reflect.Value{}, ErrInvalid
	}
	if !v.IsValid() {
		return v, nil
	}
	if v.CanInterface() {
		if _, custom := v.Interface().(json.Marshaler); custom {
			return reflect.Value{}, ErrInvalid
		}
		if _, custom := v.Interface().(encoding.TextMarshaler); custom {
			return reflect.Value{}, ErrInvalid
		}
	}
	switch v.Kind() {
	case reflect.Interface:
		out := reflect.New(v.Type()).Elem()
		if v.IsNil() {
			return out, nil
		}
		x, err := copyValue(v.Elem(), depth+1)
		if err != nil {
			return reflect.Value{}, err
		}
		out.Set(x)
		return out, nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return reflect.Value{}, ErrInvalid
		}
		if v.IsNil() {
			return reflect.Zero(v.Type()), nil
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			if !utf8.ValidString(it.Key().String()) {
				return reflect.Value{}, ErrInvalid
			}
			x, err := copyValue(it.Value(), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.SetMapIndex(it.Key(), x)
		}
		return out, nil
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return reflect.Zero(v.Type()), nil
		}
		out := reflect.New(v.Type()).Elem()
		if v.Kind() == reflect.Slice {
			out = reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		}
		for i := 0; i < v.Len(); i++ {
			x, err := copyValue(v.Index(i), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(x)
		}
		return out, nil
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(domain.Bounds{}) {
			return v, nil
		}
	case reflect.Pointer:
		if v.Type() == reflect.TypeOf((*domain.Bounds)(nil)) {
			if v.IsNil() {
				return reflect.Zero(v.Type()), nil
			}
			out := reflect.New(v.Type().Elem())
			out.Elem().Set(v.Elem())
			return out, nil
		}
	case reflect.String:
		if utf8.ValidString(v.String()) {
			return v, nil
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return v, nil
	}
	return reflect.Value{}, ErrInvalid
}

func validStrings(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return utf8.ValidString(v.String())
	case reflect.Pointer, reflect.Interface:
		return v.IsNil() || validStrings(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() && !validStrings(v.Field(i)) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if !validStrings(v.Index(i)) {
				return false
			}
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			if !validStrings(it.Key()) || !validStrings(it.Value()) {
				return false
			}
		}
	}
	return true
}
