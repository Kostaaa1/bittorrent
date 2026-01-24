package bencode

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"reflect"
)

// TODO:
// need an option to accumilate bytes that are being read (needed for info hash), also need the way of freeing the memory of the bytes
type Decoder struct {
	r *bufio.Reader
}

func NewDecoder(r io.Reader) *Decoder {
	if v, ok := r.(*bufio.Reader); ok {
		return &Decoder{r: v}
	}
	return &Decoder{r: bufio.NewReader(r)}
}

// it would be much simple to just create [2]buffer to check first 2 characters to handle leading zeros instead of doing these condition soup
func (d *Decoder) readIntBytes(delim byte) (int, error) {
	n := 0
	sign := 1
	seen := false

	for {
		b, err := d.r.ReadByte()
		if err != nil {
			if err == io.EOF {
				return 0, ErrInvalidIntegerFormat
			}
			return 0, err
		}

		// i-e
		if b == 'e' && sign == -1 && n == 0 {
			return 0, ErrInvalidIntegerFormat
		}

		if b == delim {
			return sign * n, nil
		}

		isNaN := b < '0' || b > '9'

		if isNaN && b != '-' {
			return 0, ErrInvalidIntegerFormat
		}

		if b == '-' {
			sign = -1
			if seen && sign == -1 {
				return 0, ErrInvalidIntegerFormat
			}
		}
		if seen && (b == '-' || n == 0) {
			return 0, ErrInvalidIntegerFormat
		}
		if sign == -1 && b == '0' {
			return 0, ErrInvalidIntegerFormat
		}

		if !isNaN {
			n = n*10 + int(b-'0')
			seen = true
		}
	}
}

func (d *Decoder) decodeInt(dst reflect.Value) error {
	d.r.ReadByte()

	i, err := d.readIntBytes('e')
	if err != nil {
		return err
	}

	i64 := int64(i)
	t := reflect.ValueOf(i)

	if t.Type().ConvertibleTo(dst.Type()) {
		switch dst.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if dst.OverflowInt(i64) {
				return fmt.Errorf("value=%d overflows target int type=%s", i64, dst.Type())
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			if i64 < 0 || dst.OverflowUint(uint64(i64)) {
				return fmt.Errorf("value=%d overflows target uint type=%s", i64, dst.Type())
			}
		}

		converted := t.Convert(dst.Type())
		dst.Set(converted)
	}

	return nil
}

func (d *Decoder) decodeString(dst reflect.Value) error {
	intN, err := d.readIntBytes(':')
	if err != nil {
		if errors.Is(err, ErrInvalidIntegerFormat) {
			return ErrInvalidStringFormat
		}
		return err
	}

	if intN < 0 {
		return ErrInvalidStringFormat
	}

	data := make([]byte, intN)
	if _, err := io.ReadFull(d.r, data); err != nil {
		return err
	}

	dst.SetString(string(data))

	return nil
}

func (d *Decoder) decodeList(dst reflect.Value) error {
	// 	conv, ok := data.([]interface{})
	// 	if !ok {
	// 		return errors.New("failed to decode into slice: data not slice")
	// 	}

	// 	s := reflect.MakeSlice(dst.Type(), len(conv), len(conv))

	// 	for i := 0; i < s.Len(); i++ {
	// 		if err := decodeInto(s.Index(i), conv[i]); err != nil {
	// 			return err
	// 		}
	// 	}
	// 	dst.Set(s)

	d.r.ReadByte()

	// s := reflect.MakeSlice(dst.Type(), 0, 0)

	for {
		if err := d.peekConsumeEnd(); err != nil {
			if err == errEnd {
				return nil
			}
			return err
		}

		var v interface{}
		if err := d.decode(reflect.ValueOf(&v).Elem()); err != nil {
			return err
		}

		// reflect.AppendSlice(s, reflect.ValueOf(v))
		fmt.Println("SLICE:", v)
	}
}

func (d *Decoder) peekConsumeEnd() error {
	b, err := d.r.Peek(1)
	if err != nil {
		return err
	}
	if b[0] == 'e' {
		d.r.ReadByte()
		return errEnd
	}
	return nil
}

func (d *Decoder) decodeDictToMap(dst reflect.Value) error {
	d.r.ReadByte()

	m := reflect.MakeMap(dst.Type())

	for {
		if err := d.peekConsumeEnd(); err != nil {
			if err == errEnd {
				dst.Set(m)
				return nil
			}
			return err
		}

		var key string
		if err := d.decodeString(reflect.ValueOf(&key).Elem()); err != nil {
			return err
		}

		if key == "" {
			return fmt.Errorf("failed to decode dictionary: empty key")
		}

		var value interface{}
		v := reflect.ValueOf(&value).Elem()
		if err := d.decode(v); err != nil {
			return err
		}

		m.SetMapIndex(reflect.ValueOf(key), v)
	}
}

func (d *Decoder) decode(dst reflect.Value) error {
	b, err := d.r.Peek(1)
	if err != nil {
		return err
	}

	if dst.Type().Kind() == reflect.Pointer {
		dst = dst.Elem()
	}

	fmt.Println("Decoding:", dst, dst.Type(), dst.Type().Kind())

	// if kind == reflect.Interface {
	switch b[0] {
	case 'l':
		var v []interface{}
		if err := d.decodeList(reflect.ValueOf(&v).Elem()); err != nil {
			return err
		}
		dst.Set(reflect.ValueOf(v))
		return nil
	case 'd':
		var v map[string]interface{}
		if err := d.decodeDictToMap(reflect.ValueOf(&v).Elem()); err != nil {
			return err
		}
		dst.Set(reflect.ValueOf(v))
		return nil
	case 'i':
		var v int
		if err := d.decodeInt(reflect.ValueOf(&v).Elem()); err != nil {
			return err
		}
		dst.Set(reflect.ValueOf(v))
		return nil
	default:
		var value string
		v := reflect.ValueOf(&value).Elem()
		if err := d.decodeString(v); err != nil {
			return err
		}
		dst.Set(v)
		return nil
	}
	// }
	//  else {
	// 	switch b[0] {
	// 	case 'l':
	// 		return d.decodeList(dst)
	// 	case 'd':
	// 		return d.decodeDict(dst)
	// 	case 'i':
	// 		return d.decodeInt(dst)
	// 	default:
	// 		return d.decodeString(dst)
	// 	}
	// }
}

func (d *Decoder) Decode(src interface{}) error {
	if reflect.TypeOf(src).Kind() != reflect.Pointer {
		return errors.New("src needs to be a pointer")
	}

	value := reflect.ValueOf(src)
	if err := d.decode(value); err != nil {
		return err
	}

	if d.r.Buffered() > 0 {
		value.Elem().SetZero()
		return ErrTrailingDataLeft
	}

	return nil
}

// func unmarshal(src, data interface{}) error {
// 	return decodeInto(reflect.ValueOf(src).Elem(), data)
// }

// func decodeInto(dst reflect.Value, data interface{}) error {
// 	t := dst.Type()

// 	if t.Kind() == reflect.Pointer {
// 		dst.Set(reflect.New(dst.Type().Elem()))
// 		dst = dst.Elem()
// 		t = dst.Type()
// 	}

// 	if t.Kind() == reflect.Interface {
// 		dst.Set(reflect.ValueOf(data))
// 		return nil
// 	}

// 	switch t.Kind() {
// 	case reflect.String:
// 		return decodeIntoString(dst, data)
// 	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
// 		return decodeIntoInt(dst, data)
// 	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
// 		return decodeIntoUint(dst, data)
// 	case reflect.Struct:
// 		return decodeIntoStruct(dst, data)
// 	case reflect.Slice:
// 		if t.Elem().Kind() == reflect.Uint8 {
// 			v := reflect.ValueOf(data)
// 			// if v.IsNil() {
// 			// 	return errors.New("data is nil")
// 			// }
// 			if v.Kind() != reflect.String {
// 				return fmt.Errorf("only strings are convertible to []byte")
// 			}
// 			if !v.Type().ConvertibleTo(t) {
// 				return fmt.Errorf("not convertible")
// 			}
// 			dst.Set(v.Convert(t))
// 			return nil
// 		}
// 		return decodeIntoSlice(dst, data)
// 	case reflect.Map:
// 		return decodeIntoMap(dst, data)
// 	}

// 	return errors.New("unsupported type")
// }

// func decodeIntoSlice(dst reflect.Value, data interface{}) error {
// 	conv, ok := data.([]interface{})
// 	if !ok {
// 		return errors.New("failed to decode into slice: data not slice")
// 	}

// 	s := reflect.MakeSlice(dst.Type(), len(conv), len(conv))
// 	for i := 0; i < s.Len(); i++ {
// 		if err := decodeInto(s.Index(i), conv[i]); err != nil {
// 			return err
// 		}
// 	}
// 	dst.Set(s)

// 	return nil
// }

// func decodeIntoMap(dst reflect.Value, data interface{}) error {
// 	mapped, ok := data.(map[string]interface{})
// 	if !ok {
// 		return errors.New("failed to decode into map: data not map[string]interface{}")
// 	}

// 	m := reflect.MakeMap(dst.Type())

// 	for k, v := range mapped {
// 		keyVal := reflect.ValueOf(k).Convert(dst.Type().Key())
// 		valVal := reflect.New(dst.Type().Elem()).Elem()
// 		if err := decodeInto(valVal, v); err != nil {
// 			return err
// 		}
// 		m.SetMapIndex(keyVal, valVal)
// 	}

// 	dst.Set(m)

// 	return nil
// }

// func decodeIntoStruct(dst reflect.Value, data interface{}) error {
// 	mapped, ok := data.(map[string]interface{})
// 	if !ok {
// 		return errors.New("failed to decode into struct: data not map[string]interface{}")
// 	}

// 	t := dst.Type()

// 	for i := range t.NumField() {
// 		fieldVal := dst.Field(i)
// 		fieldType := t.Field(i)

// 		if !fieldVal.CanSet() {
// 			continue
// 		}

// 		tag := fieldType.Tag.Get("bencode")
// 		if tag == "" || tag == "-" {
// 			continue
// 		}

// 		key, _ := parseTag(tag)

// 		value, ok := mapped[key]
// 		if !ok {
// 			continue
// 		}

// 		if err := decodeInto(fieldVal, value); err != nil {
// 			return err
// 		}
// 	}

// 	return nil
// }

// func decodeIntoString(dst reflect.Value, data interface{}) error {
// 	str, ok := data.(string)
// 	if !ok {
// 		return fmt.Errorf("failed decode into string: data is not string: %v", data)
// 	}
// 	dst.SetString(str)
// 	return nil
// }

// func decodeIntoInt(dst reflect.Value, data interface{}) error {
// 	n, ok := data.(int)
// 	if !ok {
// 		return fmt.Errorf("failed decode into int: data is not int: %v", data)
// 	}
// 	if dst.OverflowInt(int64(n)) {
// 		return fmt.Errorf("failed decode into int: %d overflows", dst.Kind())
// 	}
// 	dst.SetInt(int64(n))
// 	return nil
// }

// func decodeIntoUint(dst reflect.Value, data interface{}) error {
// 	n, ok := data.(int)
// 	if !ok {
// 		return fmt.Errorf("failed decode into uint: data is not uint: %v", data)
// 	}
// 	if n < 0 {
// 		return fmt.Errorf("failed decode into uint: %d is negative", dst.Kind())
// 	}
// 	if dst.OverflowUint(uint64(n)) {
// 		return fmt.Errorf("failed decode into uint: %d overflows", dst.Kind())
// 	}
// 	dst.SetUint(uint64(n))
// 	return nil
// }
