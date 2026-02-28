package peer

import "encoding/binary"

func (p *Peer) writeMsg(msg Message) error {
	if _, err := p.conn.Write(msg.Bytes()); err != nil {
		p.log.Error("write failed", "err", err)
		return err
	}
	return nil
}

func (p *Peer) sendChoke() {
	p.amChoking = true
	p.writeMsg(Message{ID: MsgChoke})
}
func (p *Peer) sendUnchoke() {
	p.amChoking = false
	p.writeMsg(Message{ID: MsgUnchoke})
}
func (p *Peer) sendInterested() {
	p.amInterested = true
	p.writeMsg(Message{ID: MsgInterested})
}
func (p *Peer) sendUninterested() {
	p.amInterested = false
	p.writeMsg(Message{ID: MsgUninterested})
}
func (p *Peer) sendKeepAlive() {
	p.writeMsg(Message{})
}
func (p *Peer) sendBitfield(bf Bitfield) {
	p.writeMsg(Message{ID: MsgBitfield, Payload: bf})
}
func (p *Peer) SendHave(pieceID int) error {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(pieceID))
	return p.writeMsg(Message{ID: MsgHave, Payload: payload})
}
func (p *Peer) sendRequest(index, begin, block int) error {
	p.log.Debug("[SEND - REQUEST]",
		"piece", index,
		"block", block,
		"peer", p.Addr,
	)
	msg := make([]byte, 12)
	binary.BigEndian.PutUint32(msg[:4], uint32(index))
	binary.BigEndian.PutUint32(msg[4:8], uint32(begin))
	binary.BigEndian.PutUint32(msg[8:12], uint32(block))
	return p.writeMsg(Message{ID: MsgRequest, Payload: msg})
}
