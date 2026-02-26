package peer

import (
	"encoding/binary"
	"fmt"
	"io"
)

const MaxBlockSize = 1 << 14 // 16KB
const MaxMessageSize = MaxBlockSize + 9

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

func (msg Message) String() string {
	switch msg.ID {
	case MsgChoke:
		return "[CHOKE]"
	case MsgUnchoke:
		return "[UNCHOKE]"
	case MsgInterested:
		return "[INTERESTED]"
	case MsgUninterested:
		return "[UNINTERESTED]"
	case MsgHave:
		return "[HAVE]"
	case MsgBitfield:
		return "[BITFIELD]"
	case MsgRequest:
		return "[REQUEST]"
	case MsgPiece:
		return "[PIECE]"
	case MsgCancel:
		return "[CANCEL]"
	case MsgPort:
		return "[PORT]"
	default:
		return "invalid"
	}
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

func readMessage(r io.Reader) (*Message, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	if length == 0 {
		return &Message{}, nil
	}

	if length > MaxMessageSize {
		return nil, fmt.Errorf("message too large: %d", length)
	}

	var idBuf [1]byte
	if _, err := io.ReadFull(r, idBuf[:]); err != nil {
		return nil, err
	}

	payload := make([]byte, length-1)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}

	return &Message{
		ID:      messageID(idBuf[0]),
		Payload: payload,
	}, nil
}

type PieceMessage struct {
	Index int
	Begin int
	Block []byte
}

func parsePieceMessage(payload []byte) PieceMessage {
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
