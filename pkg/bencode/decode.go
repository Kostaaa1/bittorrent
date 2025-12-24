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

// it would be much simple to just create [2]buffer to check first 2 characters to handle leading zeros instead of doing these condition soup
func (d *Decoder) readIntBytes(delim byte) (int, error) {
	n := 0
	sign := 1
	seenDigit := false

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
			if seenDigit && sign == -1 {
				return 0, ErrInvalidIntegerFormat
			}
		}
		if seenDigit && (b == '-' || n == 0) {
			return 0, ErrInvalidIntegerFormat
		}
		if sign == -1 && b == '0' {
			return 0, ErrInvalidIntegerFormat
		}

		if !isNaN {
			n = n*10 + int(b-'0')
			seenDigit = true
		}
	}
}

func (d *Decoder) readInt() (int, error) {
	d.r.ReadByte()
	return d.readIntBytes('e')
}

func (d *Decoder) readString() (string, error) {
	intN, err := d.readIntBytes(':')
	if err != nil {
		if errors.Is(err, ErrInvalidIntegerFormat) {
			return "", ErrInvalidStringFormat
		}
		return "", err
	}

	if intN < 0 {
		return "", ErrInvalidStringFormat
	}

	str := make([]byte, intN)
	_, err = io.ReadFull(d.r, str)
	if err != nil {
		return "", err
	}

	return string(str), nil
}

func (d *Decoder) readList() ([]interface{}, error) {
	d.r.ReadByte()
	list := make([]interface{}, 0)

	for {
		if err := d.peekConsumeEnd(); err != nil {
			if err == errEnd {
				return list, nil
			}
			return nil, err
		}

		v, err := d.decode()
		if err != nil {
			return nil, err
		}

		list = append(list, v)
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

func (d *Decoder) readDict() (map[string]interface{}, error) {
	d.r.ReadByte()
	dict := make(map[string]interface{})

	for {
		if err := d.peekConsumeEnd(); err != nil {
			if err == errEnd {
				return dict, nil
			}
			return nil, err
		}

		key, err := d.decode()
		if err != nil {
			return nil, err
		}

		k, ok := key.(string)
		if !ok {
			return nil, ErrDictKeyNotString
		}

		value, err := d.decode()
		if err != nil {
			return nil, err
		}

		dict[k] = value
	}
}

func (d *Decoder) decode() (interface{}, error) {
	b, err := d.r.Peek(1)
	if err != nil {
		return nil, err
	}

	switch b[0] {
	case 'l':
		return d.readList()
	case 'd':
		return d.readDict()
	case 'i':
		return d.readInt()
	default:
		return d.readString()
	}
}

func (d *Decoder) Decode(src interface{}) error {
	data, err := d.decode()
	if err != nil {
		return err
	}
	if d.r.Buffered() > 0 {
		return ErrTrailingDataLeft
	}
	return unmarshal(src, data)
}

func unmarshal(src, data interface{}) error {
	if reflect.TypeOf(src).Kind() != reflect.Pointer {
		return errors.New("src needs to be a pointer")
	}
	return decodeInto(reflect.ValueOf(src).Elem(), data)
}

func decodeInto(dst reflect.Value, data interface{}) error {
	t := dst.Type()

	if t.Kind() == reflect.Pointer {
		dst.Set(reflect.New(dst.Type().Elem()))
		dst = dst.Elem()
		t = dst.Type()
	}

	if t.Kind() == reflect.Interface {
		dst.Set(reflect.ValueOf(data))
		return nil
	}

	switch t.Kind() {
	case reflect.String:
		return decodeIntoString(dst, data)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return decodeIntoInt(dst, data)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return decodeIntoUint(dst, data)
	case reflect.Struct:
		return decodeIntoStruct(dst, data)
	case reflect.Slice:
		if dst.Type().Elem().Kind() == reflect.Uint8 {
			v := reflect.ValueOf(data)

			if v.IsNil() {
				return errors.New("data is nil")
			}
			if v.Kind() != reflect.String {
				return fmt.Errorf("")
			}
			if !v.Type().ConvertibleTo(t) {
				return fmt.Errorf("")
			}
		}

		return decodeIntoSlice(dst, data)
	case reflect.Map:
		return decodeIntoMap(dst, data)
	}

	return errors.New("unsupported type")
}

func decodeIntoSlice(dst reflect.Value, data interface{}) error {
	conv, ok := data.([]interface{})
	if !ok {
		return errors.New("failed to decode into slice: data not slice")
	}

	s := reflect.MakeSlice(dst.Type(), len(conv), len(conv))
	for i := 0; i < s.Len(); i++ {
		if err := decodeInto(s.Index(i), conv[i]); err != nil {
			return err
		}
	}
	dst.Set(s)

	return nil
}

func decodeIntoMap(dst reflect.Value, data interface{}) error {
	mapped, ok := data.(map[string]interface{})
	if !ok {
		return errors.New("failed to decode into map: data not map[string]interface{}")
	}

	m := reflect.MakeMap(dst.Type())

	for k, v := range mapped {
		keyVal := reflect.ValueOf(k).Convert(dst.Type().Key())
		valVal := reflect.New(dst.Type().Elem()).Elem()
		fmt.Println(valVal, valVal.Type(), valVal.Kind())

		if err := decodeInto(valVal, v); err != nil {
			return err
		}
		m.SetMapIndex(keyVal, valVal)
	}

	dst.Set(m)

	return nil
}

func decodeIntoStruct(dst reflect.Value, data interface{}) error {
	mapped, ok := data.(map[string]interface{})
	if !ok {
		return errors.New("failed to decode into struct: data not map[string]interface{}")
	}

	t := dst.Type()

	for i := range t.NumField() {
		fieldVal := dst.Field(i)
		fieldType := t.Field(i)

		if !fieldVal.CanSet() {
			continue
		}

		key := fieldType.Tag.Get("bencode")
		if key == "" {
			continue
		}

		value, ok := mapped[key]
		if !ok {
			continue
		}

		if err := decodeInto(fieldVal, value); err != nil {
			return err
		}
	}

	return nil
}

func decodeIntoString(dst reflect.Value, data interface{}) error {
	str, ok := data.(string)
	if !ok {
		return fmt.Errorf("failed decode into string: data is not string: %v", data)
	}
	dst.SetString(str)
	return nil
}

func decodeIntoInt(dst reflect.Value, data interface{}) error {
	n, ok := data.(int)
	if !ok {
		return fmt.Errorf("failed decode into int: data is not int: %v", data)
	}
	if dst.OverflowInt(int64(n)) {
		return fmt.Errorf("failed decode into int: %d overflows", dst.Kind())
	}
	dst.SetInt(int64(n))
	return nil
}

func decodeIntoUint(dst reflect.Value, data interface{}) error {
	n, ok := data.(int)
	if !ok {
		return fmt.Errorf("failed decode into uint: data is not uint: %v", data)
	}
	if n < 0 {
		return fmt.Errorf("failed decode into uint: %d is negative", dst.Kind())
	}
	if dst.OverflowUint(uint64(n)) {
		return fmt.Errorf("failed decode into uint: %d overflows", dst.Kind())
	}
	dst.SetUint(uint64(n))
	return nil
}
