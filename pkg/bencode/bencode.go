package bencode

import "errors"

var (
	errEnd                  = errors.New("end of data structure")
	ErrInvalidSyntax        = errors.New("invalid syntax")
	ErrInvalidIntegerFormat = errors.New("invalid integer format")
	ErrInvalidStringFormat  = errors.New("invalid string format")
	ErrTrailingDataLeft     = errors.New("trailing data left")
)
