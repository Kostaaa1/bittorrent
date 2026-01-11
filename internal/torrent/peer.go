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
func (p *Peer) sendRequest(index, begin int) error {
	// TODO: before requesting, we need to ask the peer if it has that piece
	fmt.Println("[REQUEST] request piece:", index, begin, p.pieceWorker.blockSize)
	payload := FormatRequest(index, begin, p.pieceWorker.blockSize)
	return p.writeMsg(Message{ID: MsgRequest, Payload: payload})
}

type Peer struct {
	PeerID string `bencode:"peer id"`
	IP     string `bencode:"ip"`
	Port   uint16 `bencode:"port"`

	conn     net.Conn
	bitfield []byte

	// Peer State
	amChoking      bool
	amInterested   bool
	peerChoking    bool
	peerInterested bool

	pieceWorker *PieceWorker
	pipeline    *requestPipeline
}

type requestPipeline struct {
	pieceIndex int
	reqSignal  chan struct{}
	windowSize int
	block      int
}

func (p *Peer) runPipeline() {
	for i := 0; i < p.pipeline.windowSize; i++ {
		p.pipeline.reqSignal <- struct{}{}
	}

	pieceIndex := 0
	offset := 0
	lastBlock := p.pieceWorker.pieceLength - p.pieceWorker.blockSize

	for {
		if p.canRequest() {
			<-p.pipeline.reqSignal
			p.sendRequest(pieceIndex, offset)

			if offset == lastBlock {
				pieceIndex += 1
				offset = 0
			} else {
				offset += p.pieceWorker.blockSize
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
			p.pipeline.reqSignal <- struct{}{}
			piece := ParsePieceMessage(msg.Payload)
			p.pieceWorker.worker <- piece
			fmt.Printf("[PIECE] index %d, begin %d, blocks %d\n", piece.index, piece.begin, len(piece.block))
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
	peer.pieceWorker = NewPieceWorker(3, tf.Pieces, tf.Files, tf.PieceLength)
	peer.pipeline = &requestPipeline{
		pieceIndex: 0,
		block:      0,
		windowSize: 4,
		reqSignal:  make(chan struct{}, 4),
	}

	if err := peer.sendInterested(); err != nil {
		return err
	}

	go peer.runPipeline()
	go peer.pieceWorker.Start()

	return peer.readMessages()
}
