package bencode

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"reflect"
)

// TODO: wrapped errors - imitate json package errors
// TODO: allow multiple same struct tags - compare struct type with bencode
// for example, allow this:
// type TrackerResponse struct {
// 	Interval int         `bencode:"interval"`
// 	Peers    interface{} `bencode:"peers"`
// 	Peers    string 	 `bencode:"peers"`
// }
// TODO: support for comparible types (e.g string -> []byte etc)

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

	n, err := d.readIntBytes('e')
	if err != nil {
		return 0, err
	}

	return n, nil
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
	t := reflect.TypeOf(src)
	if t.Kind() != reflect.Pointer {
		return errors.New("src needs to be a pointer")
	}

	p, ok := src.(*interface{})
	if ok {
		*p = data
		return nil
	}

	return decodeInto(reflect.ValueOf(src).Elem(), data)
}

func decodeInto(dst reflect.Value, src interface{}) error {
	if !dst.CanSet() {
		return fmt.Errorf("cannot set %s", dst.Type())
	}

	// Handle interface{} - just set the value directly
	if dst.Kind() == reflect.Interface {
		dst.Set(reflect.ValueOf(src))
		return nil
	}

	// Handle pointer types - allocate if nil and decode into the element
	if dst.Kind() == reflect.Ptr {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return decodeInto(dst.Elem(), src)
	}

	switch dst.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := src.(int)
		if !ok {
			return fmt.Errorf("expected int, got %T", src)
		}
		return decodeIntoInt(dst, n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, ok := src.(int)
		if !ok {
			return fmt.Errorf("expected int, got %T", src)
		}
		return decodeIntoUint(dst, n)
	case reflect.String:
		s, ok := src.(string)
		if !ok {
			return fmt.Errorf("expected string, got %T", src)
		}
		dst.SetString(s)
		return nil
	case reflect.Slice:
		// Handle []byte specially - bencode strings can be decoded into []byte
		if dst.Type().Elem().Kind() == reflect.Uint8 {
			s, ok := src.(string)
			if ok {
				dst.SetBytes([]byte(s))
				return nil
			}
		}
		list, ok := src.([]interface{})
		if !ok {
			return fmt.Errorf("expected list, got %T", src)
		}
		return decodeIntoSlice(dst, list)
	case reflect.Map:
		dict, ok := src.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected dict, got %T", src)
		}
		return decodeIntoMap(dst, dict)
	case reflect.Struct:
		dict, ok := src.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected dict, got %T", src)
		}
		return decodeIntoStruct(dst, dict)
	}

	return fmt.Errorf("unsupported decode type: %s", dst.Kind())
}

func decodeIntoInt(value reflect.Value, n int) error {
	n64 := int64(n)
	if value.OverflowInt(n64) {
		return fmt.Errorf("overflow %d for %s", n, value.Type())
	}
	value.SetInt(n64)
	return nil
}

func decodeIntoUint(value reflect.Value, n int) error {
	if n < 0 {
		return fmt.Errorf("negative %d for %s", n, value.Type())
	}
	if value.OverflowUint(uint64(n)) {
		return fmt.Errorf("overflow %d for %s", n, value.Type())
	}
	value.SetUint(uint64(n))
	return nil
}

func decodeIntoStruct(dst reflect.Value, src map[string]interface{}) error {
	t := dst.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldVal := dst.Field(i)

		if !fieldVal.CanSet() {
			continue
		}

		key := field.Tag.Get("bencode")
		if key == "" {
			key = field.Name
		}
		if key == "-" {
			continue
		}

		value, ok := src[key]
		if !ok {
			continue
		}

		if err := decodeInto(fieldVal, value); err != nil {
			return err
		}
	}
	return nil
}

func decodeIntoSlice(dst reflect.Value, src []interface{}) error {
	sliceValue := reflect.MakeSlice(dst.Type(), len(src), len(src))
	for i := 0; i < len(src); i++ {
		if err := decodeInto(sliceValue.Index(i), src[i]); err != nil {
			return err
		}
	}
	dst.Set(sliceValue)
	return nil
}

func decodeIntoMap(dst reflect.Value, src map[string]interface{}) error {
	if dst.IsNil() {
		dst.Set(reflect.MakeMap(dst.Type()))
	}

	keyType := dst.Type().Key()
	valType := dst.Type().Elem()

	for k, v := range src {
		keyVal := reflect.New(keyType).Elem()
		if keyType.Kind() == reflect.String {
			keyVal.SetString(k)
		} else {
			return fmt.Errorf("map key must be string, got %s", keyType)
		}

		valVal := reflect.New(valType).Elem()
		if err := decodeInto(valVal, v); err != nil {
			return err
		}

		dst.SetMapIndex(keyVal, valVal)
	}
	return nil
}
