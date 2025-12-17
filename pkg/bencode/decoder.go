package bencode

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

type Decoder struct {
	r *bufio.Reader
}

var (
	ErrInvalidSyntax        = errors.New("bencode: invalid syntax")
	ErrInvalidIntegerFormat = errors.New("bencode: invalid integer format")
	ErrInvalidStringFormat  = errors.New("bencode: invalid string format")
	ErrTrailingDataLeft     = errors.New("bencode: trailing data left")
)

func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReader(r)}
}

func (d *Decoder) readUntilDelim(delim byte, numsOnly bool) ([]byte, error) {
	n := make([]byte, 0)
	isNumberValid := false

	for {
		b, err := d.r.ReadByte()
		if err != nil {
			return nil, err
		}

		if b == delim {
			break
		}

		n = append(n, b)

		if numsOnly {
			if isNaN(b) && b != '-' {
				return nil, ErrInvalidIntegerFormat
			}
			if b == '-' && len(n) > 1 {
				return nil, ErrInvalidIntegerFormat
			}
			if !isNumberValid && len(n) >= 2 {
				zeros := n[0] == '0' && n[1] >= '0'   // 00
				negZero := n[0] == '-' && n[1] <= '0' // -0
				if zeros || negZero {
					return nil, ErrInvalidIntegerFormat
				}
				isNumberValid = true
			}
		}
	}

	return n, nil
}

func (d *Decoder) readInt() (int, error) {
	// consume i
	d.r.ReadByte()

	n, err := d.readUntilDelim('e', true)
	if err != nil {
		if err == io.EOF {
			return 0, ErrInvalidIntegerFormat
		}
		return 0, err
	}

	intN, err := bytesToInt(n)
	if err != nil {
		return 0, ErrInvalidIntegerFormat
	}

	return intN, nil
}

func (d *Decoder) readString() (string, error) {
	rest, err := d.readUntilDelim(':', true)
	if err != nil {
		return "", err
	}

	intN, err := bytesToInt(rest)
	if err != nil {
		return "", err
	}

	if intN < 0 {
		return "", ErrInvalidIntegerFormat
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
		v, err := d.decode()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		list = append(list, v)
	}

	return list, nil
}

func (d *Decoder) readDict() (map[string]interface{}, error) {
	d.r.ReadByte()
	dict := make(map[string]interface{})

	for {
		key, err := d.decode()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		k, ok := key.(string)
		if !ok {
			return nil, errors.New("key is not string?")
		}

		value, err := d.decode()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		dict[k] = value
	}

	return dict, nil
}

func (d *Decoder) decode() (interface{}, error) {
	b, err := d.r.Peek(1)
	if err != nil {
		// if err == io.EOF {
		// 	return nil, ErrInvalidSyntax
		// }
		return nil, err
	}

	switch b[0] {
	case 'l':
		return d.readList()
	case 'd':
		return d.readDict()
	case 'i':
		return d.readInt()
	case 'e':
		d.r.ReadByte()
		return nil, io.EOF
	default:
		return d.readString()
	}
}

func (d *Decoder) Decode() (interface{}, error) {
	data, err := d.decode()
	if err != nil {
		return nil, err
	}
	fmt.Println("DATA: ", data)

	if d.r.Buffered() > 0 {
		return nil, ErrTrailingDataLeft
	}

	return data, nil
}
