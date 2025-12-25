package torrent

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

type Message struct {
	ID      byte
	Payload []byte
}

func (msg *Message) Serialize() []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, 1+len(msg.Payload))
	buf.WriteByte(msg.ID)
	buf.Write(msg.Payload)
	return buf.Bytes()
}

func ReadMessage(r io.Reader) (*Message, error) {
	var length uint32

	err := binary.Read(r, binary.BigEndian, &length)
	if err != nil {
		return nil, err
	}

	if length == 0 {
		return nil, nil
	}

	var b [1]byte
	if _, err = io.ReadFull(r, b[:]); err != nil {
		return nil, err
	}
	msg := new(Message)

	msg.ID = b[0]
	msg.Payload = make([]byte, length-1)

	_, err = io.ReadFull(r, msg.Payload)
	if err != nil {
		return nil, err
	}

	fmt.Println("Message:", msg.ID, msg.Payload)

	return msg, nil
}
