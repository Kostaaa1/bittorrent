package bencode

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
)

var (
	ErrUnsupportedEncodeType = errors.New("unsupported encode type")
)

type encoder struct {
	w io.Writer
}

func NewEncoder(w io.Writer) *encoder {
	return &encoder{w: w}
}

func Marshal(v any) ([]byte, error) {
	buf := new(bytes.Buffer)
	enc := &encoder{w: buf}
	if err := enc.encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (e *encoder) Encode(v any) error {
	return e.encode(v)
}

func (e *encoder) write(b []byte) error {
	_, err := e.w.Write(b)
	if err != nil {
		return err
	}
	return nil
}

func (e *encoder) writeStr(v string) error {
	s := fmt.Sprintf("%d:%s", len(v), v)
	return e.write([]byte(s))
}

func (e *encoder) writeInt(v int64) error {
	s := fmt.Sprintf("i%de", v)
	return e.write([]byte(s))
}

func (e *encoder) writeMap(v reflect.Value) error {
	keys := v.MapKeys()
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].String() < keys[j].String()
	})

	if err := e.write([]byte("d")); err != nil {
		return err
	}

	for _, key := range keys {
		elem := v.MapIndex(key)
		if elem.Kind() == reflect.Interface || elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		if key.Kind() != reflect.String {
			return ErrDictKeyNotString
		}
		if err := e.writeStr(key.String()); err != nil {
			return err
		}
		if err := e.encode(elem); err != nil {
			return err
		}
	}

	return e.write([]byte("e"))
}

func (e *encoder) writeSlice(v reflect.Value) error {
	if err := e.write([]byte("l")); err != nil {
		return err
	}
	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		if elem.Kind() == reflect.Interface || elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		if err := e.encode(elem); err != nil {
			return err
		}
	}
	return e.write([]byte("e"))
}

func structToMap(value reflect.Value) reflect.Value {
	reflectedMap := reflect.MakeMap(reflect.TypeOf(map[string]any{}))

	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		fieldValue := value.Field(i)
		if field.Anonymous || field.PkgPath != "" {
			continue
		}
		key := field.Name
		if tag, ok := field.Tag.Lookup("bencode"); ok {
			if tag == "-" {
				continue
			}
			key = tag
		}
		reflectedMap.SetMapIndex(reflect.ValueOf(key), fieldValue)
	}

	return reflectedMap
}

func (e *encoder) encode(v any) error {
	var value reflect.Value

	switch t := v.(type) {
	case reflect.Value:
		value = t
	default:
		value = reflect.ValueOf(v)
	}

	switch value.Kind() {
	case reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64:
		return e.writeInt(value.Int())
	case reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64:
		return e.writeInt(int64(value.Uint()))
	case reflect.String:
		return e.writeStr(value.String())
	case reflect.Slice:
		return e.writeSlice(value)
	case reflect.Map:
		return e.writeMap(value)
	case reflect.Struct:
		return e.writeMap(structToMap(value))
	}

	return ErrUnsupportedEncodeType
}
