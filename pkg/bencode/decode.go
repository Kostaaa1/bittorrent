package bencode

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"reflect"
)

type Decoder struct {
	r *bufio.Reader
}

func NewDecoder(r io.Reader) *Decoder {
	if v, ok := r.(*bufio.Reader); ok {
		return &Decoder{r: v}
	}
	return &Decoder{r: bufio.NewReader(r)}
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

		dst.Set(t.Convert(dst.Type()))
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
	d.r.ReadByte()

	fmt.Println("decoding slice", dst, dst.Type())
	s := reflect.MakeSlice(dst.Type(), 0, 0)

	for {
		if err := d.peekConsumeEnd(); err != nil {
			if err == errEnd {
				dst.Set(s)
				return nil
			}
			return err
		}

		var v interface{}
		if err := d.decode(reflect.ValueOf(&v).Elem()); err != nil {
			return err
		}

		s = reflect.Append(s, reflect.ValueOf(v))
	}
}

func (d *Decoder) decodeDictionaryToStruct(dst reflect.Value, key string) error {
	for i := range dst.NumField() {
		fieldVal := dst.Field(i)
		fieldType := dst.Type().Field(i)

		if !fieldVal.CanSet() {
			continue
		}

		tag := fieldType.Tag.Get("bencode")
		if tag == "" || tag == "-" {
			continue
		}

		name, omitempty := parseTag(tag)
		_ = omitempty

		if name == key {
			if err := d.decode(fieldVal); err != nil {
				return err
			}
			break
		}
	}

	return nil
}

func (d *Decoder) decodeDictionary(dst reflect.Value) error {
	d.r.ReadByte()

	fmt.Println(dst, dst.Type(), dst.Type().Kind())

	var m reflect.Value
	if dst.Type().Kind() == reflect.Map {
		m = reflect.MakeMap(dst.Type())
	}

	for {
		if err := d.peekConsumeEnd(); err != nil {
			if err == errEnd {
				if dst.Type().Kind() == reflect.Map {
					dst.Set(m)
				}
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

		switch dst.Type().Kind() {
		case reflect.Struct:
			if err := d.decodeDictionaryToStruct(dst, key); err != nil {
				return err
			}
		case reflect.Map:
			if err := d.decodeDictionaryToMap(m, key); err != nil {
				return err
			}
		}
	}
}

func (d *Decoder) decodeDictionaryToMap(m reflect.Value, key string) error {
	var value interface{}
	v := reflect.ValueOf(&value).Elem()
	if err := d.decode(v); err != nil {
		return err
	}
	m.SetMapIndex(reflect.ValueOf(key), v)
	return nil
}

func (d *Decoder) decodeToInterface(b byte, dst reflect.Value) error {
	var v any

	switch b {
	case 'l':
		var tmp []interface{}
		if err := d.decodeList(reflect.ValueOf(&tmp).Elem()); err != nil {
			return err
		}
		v = tmp
	case 'd':
		var tmp map[string]interface{}
		if err := d.decodeDictionary(reflect.ValueOf(&tmp).Elem()); err != nil {
			return err
		}
		v = tmp
	case 'i':
		var tmp int
		if err := d.decodeInt(reflect.ValueOf(&tmp).Elem()); err != nil {
			return err
		}
		v = tmp
	default:
		var tmp string
		if err := d.decodeString(reflect.ValueOf(&tmp).Elem()); err != nil {
			return err
		}
		v = tmp
	}

	dst.Set(reflect.ValueOf(v))
	return nil
}

func (d *Decoder) decode(dst reflect.Value) error {
	b, err := d.r.Peek(1)
	if err != nil {
		return err
	}

	if dst.Type().Kind() == reflect.Pointer {
		dst = dst.Elem()
	}

	if dst.Type().Kind() == reflect.Interface {
		return d.decodeToInterface(b[0], dst)
	}

	switch b[0] {
	case 'l':
		return d.decodeList(dst)
	case 'd':
		return d.decodeDictionary(dst)
	case 'i':
		return d.decodeInt(dst)
	default:
		return d.decodeString(dst)
	}
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
