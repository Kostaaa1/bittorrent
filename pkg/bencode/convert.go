package bencode

import (
	"errors"
	"fmt"
)

func isNaN(b byte) bool {
	return b < '0' || b > '9'
}

func bytesToInt(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, errors.New("input is empty")
	}

	sign := 1
	start := 0

	if b[0] == '-' {
		sign = -1
		start = 1
		if len(b) == 1 {
			return 0, fmt.Errorf("invalid number")
		}
	}

	n := 0

	for _, c := range b[start:] {
		if isNaN(c) {
			return 0, errors.New("invalid syntax")
		}
		n = n*10 + int(c-'0')
	}

	return sign * n, nil
}
