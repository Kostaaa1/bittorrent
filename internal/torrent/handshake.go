package torrent

import (
	"bufio"
	"fmt"
	"io"
)

type Handshake struct {
	// Pstrlen   int      `bencode:"pstrlen"`
	Pstr      []byte   `bencode:"pstrlen"`
	Reserverd [8]byte  `bencode:"reserved"`
	InfoHash  [20]byte `bencode:"info_hash"`
	PeerID    [20]byte `bencode:"peer_id"`
}

func (hs *Handshake) Bytes() []byte {
	buf := make([]byte, 49+len(hs.Pstr))
	buf[0] = byte(len(hs.Pstr))
	curr := 1
	curr += copy(buf[curr:], []byte(hs.Pstr))
	curr += copy(buf[curr:], hs.Reserverd[:])
	curr += copy(buf[curr:], hs.InfoHash[:])
	curr += copy(buf[curr:], hs.PeerID[:])
	return buf
}

func (hs *Handshake) Read(reader io.Reader) error {
	// hs := new(Handshake)
	r := bufio.NewReader(reader)

	length, err := r.ReadByte()
	if err != nil {
		return err
	}

	l := int(length)

	if length <= 0 {
		return fmt.Errorf("length cannot be less then zero: %d", l)
	}

	hs.Pstr = make([]byte, l)

	n, err := r.Read(hs.Pstr)
	if err != nil {
		return err
	}

	if n != l {
		return fmt.Errorf("did not read expected amount: read %d, expected %d", n, l)
	}

	n, err = r.Read(hs.Reserverd[:])
	if err != nil {
		return err
	}
	if n != 8 {
		return fmt.Errorf("did not read expected amount: read %d, expected %d", n, 8)
	}

	n, err = r.Read(hs.InfoHash[:])
	if err != nil {
		return err
	}
	if n != 20 {
		return fmt.Errorf("did not read expected amount: read %d, expected %d", n, 20)
	}

	n, err = r.Read(hs.PeerID[:])
	if err != nil {
		return err
	}
	if n != 20 {
		return fmt.Errorf("did not read expected amount: read %d, expected %d", n, 20)
	}

	return nil
}
