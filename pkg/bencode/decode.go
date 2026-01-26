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

		// if sign == -1 && b == '0' {
		// 	return 0, ErrInvalidIntegerFormat
		// }

		if !isNaN {
			n = n*10 + int(b-'0')
			seen = true
		}
	}
}

func (d *Decoder) decodeInt(dst reflect.Value) error {
	_, err := d.r.ReadByte()
	if err != nil {
		return err
	}

	i, err := d.readIntBytes('e')
	if err != nil {
		return err
	}

	i64 := int64(i)

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

	v := reflect.ValueOf(i)
	vt := v.Type()
	dt := dst.Type()

	if vt != dt && vt.ConvertibleTo(dt) {
		dst.Set(v.Convert(dt))
	} else {
		dst.Set(v)
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

	isByteSlice := dst.Kind() == reflect.Slice && dst.Type().Elem().Kind() == reflect.Uint8

	if isByteSlice {
		dst.Set(reflect.ValueOf(data))
	} else {
		dst.SetString(string(data))
	}

	return nil
}

func (d *Decoder) decodeList(dst reflect.Value) error {
	d.r.ReadByte()

	s := reflect.MakeSlice(dst.Type(), 0, 0)
	elemType := dst.Type().Elem()

	for {
		if err := d.peekConsumeEnd(); err != nil {
			if err == errEnd {
				dst.Set(s)
				return nil
			}
			return err
		}

		v := reflect.New(elemType).Elem()
		if err := d.decode(v); err != nil {
			return err
		}

		s = reflect.Append(s, v)
	}
}

// TODO: improve the search for key? avoid iterating everytime? maybe index tracking, then fallback to iteration
func (d *Decoder) decodeDictionaryToStruct(dst reflect.Value, key string) error {
	valueDecoded := false

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
			valueDecoded = true
			if err := d.decode(fieldVal); err != nil {
				return err
			}
			break
		}
	}

	// If the struct field does not exist (i.e., the destination does not have a corresponding field), but the input stream contains a valid value for it, we still need to consume/parse the value from the byte stream. Otherwise, the decoder would get out of sync and subsequent reads could fail or return incorrect data	 To handle this, we decode the value into a temporary `interface{}` variable. This allows the decoder to read and discard the data while maintaining the correct position in the input stream. Optionally, a "Strict" mode could be implemented to treat unknown fields as an error instead of silently skipping them.
	if !valueDecoded {
		var tmp interface{}
		if err := d.decode(reflect.ValueOf(&tmp).Elem()); err != nil {
			return err
		}
	}

	return nil
}

func (d *Decoder) decodeDictionary(dst reflect.Value) error {
	d.r.ReadByte()

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
			elemType := dst.Type().Elem()

			value := reflect.New(elemType).Elem()
			if err := d.decode(value); err != nil {
				return err
			}

			m.SetMapIndex(reflect.ValueOf(key), value)
		default:
			return fmt.Errorf("invalid dictionary type: %s", dst.Kind())
		}
	}
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

func (d *Decoder) Decode(src interface{}) (err error) {
	if reflect.TypeOf(src).Kind() != reflect.Pointer {
		return errors.New("src needs to be a pointer")
	}

	value := reflect.ValueOf(src)

	defer func() {
		if err != nil {
			value.Elem().SetZero()
		}
	}()

	if err = d.decode(value); err != nil {
		return
	}

	if d.r.Buffered() > 0 {
		err = ErrTrailingDataLeft
		return
	}

	return
}
