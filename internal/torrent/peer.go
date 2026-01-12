package torrent

import (
	"fmt"
	"io"
	"net"
	"time"
)

func (p *Peer) writeMsg(msg Message) error {
	if _, err := p.conn.Write(msg.Bytes()); err != nil {
		return err
	}
	return nil
}
func (p *Peer) sendChoke() error {
	p.amChoking = true
	return p.writeMsg(Message{ID: MsgChoke})
}
func (p *Peer) sendUnchoke() error {
	p.amChoking = false
	return p.writeMsg(Message{ID: MsgUnchoke})
}
func (p *Peer) sendInterested() error {
	p.amInterested = true
	return p.writeMsg(Message{ID: MsgInterested})
}
func (p *Peer) sendUninterested() error {
	p.amInterested = false
	return p.writeMsg(Message{ID: MsgUninterested})
}
func (p *Peer) sendHave() error {
	// return p.writeMsg(Message{ID: MsgHave, Payload: []byte()})
	return nil
}
func (p *Peer) sendRequest(index, begin, block int) error {
	// TODO: before requesting, we need to ask the peer if it has that piece
	fmt.Println("[REQUEST] request piece:", index, begin, block)
	payload := FormatRequest(index, begin, block)
	return p.writeMsg(Message{ID: MsgRequest, Payload: payload})
}

type Peer struct {
	PeerID         string `bencode:"peer id"`
	IP             string `bencode:"ip"`
	Port           uint16 `bencode:"port"`
	conn           net.Conn
	bitfield       []byte
	amChoking      bool
	amInterested   bool
	peerChoking    bool
	peerInterested bool
	writer         *PieceWriter
	pipeline       *requestPipeline
}

type requestPipeline struct {
	reqSignal  chan struct{}
	windowSize int
}

func (p *Peer) runPipeline() {
	for i := 0; i < p.pipeline.windowSize; i++ {
		p.pipeline.reqSignal <- struct{}{}
	}

	lastPieceID := p.writer.numOfPieces - 1
	lastBlockID := p.writer.pieceLength - p.writer.blockSize

	currPiece := 0
	currOffset := 0

	for {
		if p.canRequest() {
			<-p.pipeline.reqSignal

			if currOffset == lastBlockID {
				if currPiece == lastPieceID {
					remainder := int(p.writer.totalLength) - currOffset - (lastPieceID * p.writer.pieceLength)
					p.sendRequest(currPiece, currOffset, remainder)
				} else {
					p.sendRequest(currPiece, currOffset, p.writer.blockSize)
					currPiece += 1
					currOffset = 0
				}
			} else {
				p.sendRequest(currPiece, currOffset, p.writer.blockSize)
				currOffset += p.writer.blockSize
			}
		}
	}
}

func (p *Peer) canRequest() bool {
	return p.amInterested && !p.peerChoking
}

func (p *Peer) ip4addr() string {
	return fmt.Sprintf("%s:%d", p.IP, p.Port)
}

func (p *Peer) Close() error {
	return p.conn.Close()
}

func (p *Peer) readMessages() error {
	for {
		msg, err := ReadMessage(p.conn)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		switch msg.ID {
		case MsgChoke:
			fmt.Println("[MESSAGE] choke")
			p.peerChoking = true
			// clear pending requests
			// notify piece Worker that pending requests are dead
		case MsgUnchoke:
			fmt.Println("[MESSAGE] unchoke")
			p.peerChoking = false
		case MsgInterested:
			fmt.Println("[MESSAGE] interested")
			p.peerInterested = true
		case MsgUninterested:
			fmt.Println("[MESSAGE] uninterested")
			p.peerInterested = false
		case MsgBitfield:
			fmt.Println("[MESSAGE] bitfield", msg.Payload)
			p.bitfield = msg.Payload
		case MsgRequest:
			fmt.Println("[MESSAGE] request")
			p.bitfield = msg.Payload
		case MsgPiece:
			piece := ParsePieceMessage(msg.Payload)
			fmt.Printf("[PIECE] index %d, begin %d, blocks %d\n", piece.index, piece.begin, len(piece.block))
			p.pipeline.reqSignal <- struct{}{}
			p.writer.worker <- piece
		case MsgHave:
			fmt.Println("[MESSAGE] have")
		case MsgCancel:
			fmt.Println("[MESSAGE] cancel")
		case MsgPort:
			fmt.Println("[MESSAGE] port")
		}
	}
}

func (p *Peer) handshake(hs *Handshake) error {
	_, err := p.conn.Write(hs.Bytes())
	if err != nil {
		return fmt.Errorf("failed to write handshake: %v", err)
	}
	peerHandshake, err := ReadHandshake(p.conn)
	if err != nil {
		return fmt.Errorf("failed to read handshake: %v", err)
	}
	if peerHandshake.InfoHash != hs.InfoHash {
		return fmt.Errorf("failed to validate handshakes for peer: %s", p.ip4addr())
	}
	return nil
}

func (peer *Peer) DialWithHandshake(hs *Handshake, tf *TorrentFile) error {
	addr := peer.ip4addr()
	fmt.Println("Dialing peer:", addr)

	conn, err := net.DialTimeout("tcp", addr, time.Second*30)
	if err != nil {
		return fmt.Errorf("failed to dial: %v", err)
	}

	peer.conn = conn
	if err := peer.handshake(hs); err != nil {
		return err
	}

	fmt.Println("Handshake successful with peer:", addr)

	peer.amInterested = false
	peer.peerChoking = true
	peer.writer = NewPieceWriter(3, tf.Pieces, tf.Files, tf.TotalLength, tf.PieceLength, len(tf.Pieces))

	peer.pipeline = &requestPipeline{
		windowSize: 4,
		reqSignal:  make(chan struct{}, 4),
	}

	if err := peer.sendInterested(); err != nil {
		return err
	}

	go peer.runPipeline()
	go peer.writer.Start()

	return peer.readMessages()
}
