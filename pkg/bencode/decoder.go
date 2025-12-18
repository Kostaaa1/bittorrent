package bencode

import (
	"bufio"
	"errors"
	"io"
)

type Decoder struct {
	r *bufio.Reader
}

var (
	ErrDictKeyNotString     = errors.New("dictionary key is not string")
	errEnd                  = errors.New("end of data structure")
	ErrInvalidSyntax        = errors.New("invalid syntax")
	ErrInvalidIntegerFormat = errors.New("invalid integer format")
	ErrInvalidStringFormat  = errors.New("invalid string format")
	ErrTrailingDataLeft     = errors.New("trailing data left")
)

func NewDecoder(r io.Reader) *Decoder {
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

		isNum := !isNaN(b)

		if !isNum && b != '-' {
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

		if isNum {
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

		v, err := d.decodeValue()
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

		key, err := d.decodeValue()
		if err != nil {
			return nil, err
		}

		k, ok := key.(string)
		if !ok {
			return nil, ErrDictKeyNotString
		}

		value, err := d.decodeValue()
		if err != nil {
			return nil, err
		}

		dict[k] = value
	}
}

func (d *Decoder) decodeValue() (interface{}, error) {
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

func (d *Decoder) Decode(src interface{}) (err error) {
	data, err := d.decodeValue()
	if err != nil {
		return err
	}
	if d.r.Buffered() > 0 {
		return ErrTrailingDataLeft
	}

	p, ok := src.(*interface{})
	if !ok {
		return err
	}
	*p = data

	return nil
}
