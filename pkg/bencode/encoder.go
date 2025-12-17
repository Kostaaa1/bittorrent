package bencode

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
)

type primitives int

const (
	typeStr primitives = iota
	typeInt
	typeList
	typeDict
)

type encoder struct {
	w io.Writer
}

func (e *encoder) writeStr(v string) {
	s := fmt.Sprintf("%d:%s", len(v), v)
	e.w.Write([]byte(s))
}

func (e *encoder) writeInt(v int64) {
	s := fmt.Sprintf("i%de", v)
	e.w.Write([]byte(s))
}

func (e *encoder) writeSlice(v reflect.Value) {
	e.w.Write([]byte("l"))
	for i := 0; i < v.Len(); i++ {
		value := v.Index(i)
		e.encode(value)
	}
	e.w.Write([]byte("e"))
}

func reflectValue(v any) reflect.Value {
	var value reflect.Value
	switch t := v.(type) {
	case reflect.Value:
		value = t
	default:
		value = reflect.ValueOf(v)
	}
	return value
}

func (e *encoder) encode(v any) error {
	value := reflectValue(v)
	// first check if v is not valid type to avoid return nil in switch cases

	fmt.Println("KIND: ", v, value.Kind())

	// switch s := v.(type) {
	// case []byte:
	// }

	switch value.Kind() {
	case reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64:
		e.writeInt(value.Int())
		return nil
	case reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64:
		e.writeInt(int64(value.Uint()))
		return nil
	case reflect.String:
		e.writeStr(value.String())
		return nil
	case reflect.Slice:
		e.writeSlice(value)
		return nil
	}

	return errors.New("failed to encode")
}

// detect cycles / stack overflows
// NaN, +Inf, -Inf

func Encode(v any) ([]byte, error) {
	buf := new(bytes.Buffer)
	enc := &encoder{w: buf}
	if err := enc.encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
