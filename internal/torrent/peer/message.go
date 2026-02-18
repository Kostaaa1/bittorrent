package peer

import (
	"encoding/binary"
	"io"
)

type messageID byte

const (
	MsgChoke messageID = iota
	MsgUnchoke
	MsgInterested
	MsgUninterested
	MsgHave
	MsgBitfield
	MsgRequest
	MsgPiece
	MsgCancel
	MsgPort
)

type Message struct {
	ID      messageID
	Payload []byte
}

func (msg *Message) Bytes() []byte {
	if msg == nil {
		return make([]byte, 4)
	}
	length := uint32(len(msg.Payload) + 1)
	buf := make([]byte, length+4)
	binary.BigEndian.PutUint32(buf, length)
	buf[4] = byte(msg.ID)
	copy(buf[5:], msg.Payload)
	return buf
}

func ReadMessage(r io.Reader) (*Message, error) {
	var length uint32

	err := binary.Read(r, binary.BigEndian, &length)
	if err != nil {
		return nil, err
	}

	if length == 0 {
		return &Message{}, nil
	}

	var b [1]byte
	if _, err = io.ReadFull(r, b[:]); err != nil {
		return nil, err
	}

	id := b[0]

	payload := make([]byte, length-1)

	_, err = io.ReadFull(r, payload)
	if err != nil {
		return nil, err
	}

	return &Message{
		ID:      messageID(id),
		Payload: payload,
	}, nil
}

type PieceMessage struct {
	Index int
	Begin int
	Block []byte
}

func ParsePieceMessage(payload []byte) PieceMessage {
	index := binary.BigEndian.Uint32(payload[:4])
	begin := binary.BigEndian.Uint32(payload[4:8])
	block := make([]byte, len(payload[8:]))
	copy(block, payload[8:])
	return PieceMessage{
		Index: int(index),
		Begin: int(begin),
		Block: block,
	}
}
