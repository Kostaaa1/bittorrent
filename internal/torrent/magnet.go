package torrent

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type MagnetLink struct {
	ExactTopic  string
	InfoHash    [20]byte
	DisplayName string
	Trackers    []string
	ExactLength *int
	WebSeed     *string
	// TODO: this needs to be parsed as []int i think
	SelectOnly string
}

func parseExactTopic(xt string) ([20]byte, error) {
	prefix := "urn:btih:"
	hash := strings.Trim(xt, prefix)

	var hashBytes [20]byte

	n, err := hex.Decode(hashBytes[:], []byte(hash))
	if err != nil {
		return [20]byte{}, err
	}

	if n != 20 {
		return [20]byte{}, fmt.Errorf("failed to parse magnet link: info hash length is not 20: length=%d", n)
	}

	return hashBytes, nil
}

func ParseMagnetLink(ml string) (*MagnetLink, error) {
	url, err := url.Parse(ml)
	if err != nil {
		panic(err)
	}

	qp := url.Query()

	xt := qp.Get("xt")
	dn := qp.Get("dn")
	so := qp.Get("so")
	xl := qp.Get("xl")
	// ws := qp.Get("ws")
	trackers := qp["tr"]

	if xt == "" {
		return nil, fmt.Errorf("failed to parse magnet link: missing exact topic")
	}
	if dn == "" {
		return nil, fmt.Errorf("failed to parse magnet link: missing display name")
	}
	if len(trackers) == 0 {
		return nil, fmt.Errorf("failed to parse magnet link: no trackers found")
	}

	var exactLength *int
	if xl != "" {
		xl, err := strconv.Atoi(xl)
		if err != nil {
			return nil, err
		}
		exactLength = &xl
	}

	hashBytes, err := parseExactTopic(xt)
	if err != nil {
		return nil, err
	}

	return &MagnetLink{
		ExactTopic:  xt,
		DisplayName: dn,
		InfoHash:    hashBytes,
		Trackers:    trackers,
		SelectOnly:  so,
		ExactLength: exactLength,
	}, nil
}
