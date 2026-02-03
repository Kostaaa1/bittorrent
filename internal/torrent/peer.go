package torrent

import (
	"fmt"
	"io"
	"net"
)

type Peer struct {
	ID       int
	conn     net.Conn
	bitfield []byte

	// peer state
	amChoking      bool
	amInterested   bool
	peerChoking    bool
	peerInterested bool

	pipeline      *Pipeline
	writer        chan<- PieceMessage
	assignedPiece *Piece

	// writer   *PieceWriter
	// pipeline *requestPipeline

	numOfPieces       int
	totalLength       int
	pieceLength       int
	blockSize         int
	numBlocksPerPiece int8
}

type Pipeline struct {
	windowSize      int
	window          chan struct{}
	requested       int
	maxRequestCount int8
}

func NewPipeline(windowSize int, maxRequestCount int8) *Pipeline {
	return &Pipeline{
		windowSize:      windowSize,
		window:          make(chan struct{}, windowSize),
		requested:       0,
		maxRequestCount: maxRequestCount,
	}
}

func (peer *Peer) HasPiece(index int) bool {
	if len(peer.bitfield) == 0 {
		return true
	}
	byteIndex := index / 8
	offset := index % 8
	return peer.bitfield[byteIndex]>>(7-offset)&1 != 0
}

func (peer *Peer) StartRequestPipeline() {
	for i := 0; i < peer.pipeline.windowSize; i++ {
		peer.pipeline.window <- struct{}{}
	}

	lastPieceID := peer.numOfPieces - 1
	lastBlockID := peer.pieceLength - peer.blockSize

	for {
		if peer.assignedPiece != nil &&
			peer.pipeline.requested*peer.blockSize < peer.pieceLength &&
			peer.canRequest() {

			<-peer.pipeline.window

			// if last block of last piece: calculate block size
			// TODO: fix piece offset calc

			index := peer.assignedPiece.index
			block := peer.blockSize
			begin := peer.pipeline.requested * block

			if lastPieceID == index && lastBlockID == begin {
				block = peer.totalLength - begin - (lastPieceID * peer.pieceLength)
				fmt.Println("CALCULATED LAST BLOCK:", block)
			}

			peer.sendRequest(index, begin, block)

			// blockSize := peer.blockSize
			// if currOffset == lastBlockID {
			// 	if currPiece == lastPieceID {
			// 		blockSize = peer.totalLength - currOffset - (lastPieceID * peer.pieceLength)
			// 	} else {
			// 		currPiece += 1
			// 		currOffset = 0
			// 	}
			// } else {
			// 	currOffset += peer.blockSize
			// }
			// if peer.assignedPiece.state != PieceAssigned {
			// 	peer.assignedPiece.state = PieceAssigned
			// }
			// peer.sendRequest(currPiece, currOffset, blockSize)
		}
	}
}

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
	fmt.Println("sending interested to peer", p.conn.RemoteAddr())
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

	p.pipeline.requested++
	if p.assignedPiece.state != PieceInFlight {
		p.assignedPiece.state = PieceInFlight
	}

	payload := FormatRequest(index, begin, block)
	return p.writeMsg(Message{ID: MsgRequest, Payload: payload})
}

func (p *Peer) canRequest() bool {
	return p.amInterested && !p.peerChoking
}

func (p *Peer) Close() error {
	return p.conn.Close()
}

func (p *Peer) ReadMessages() error {
	fmt.Println("Reading messages for peer:", p.conn.RemoteAddr())
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
			// clear pending requests
			// notify piece Worker that pending requests are dead
			fmt.Println("[MESSAGE] choke")
			p.peerChoking = true
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
			fmt.Printf("[MESSAGE] piece: index %d, begin %d, blocks %d\n", piece.index, piece.begin, len(piece.block))

			p.pipeline.window <- struct{}{}
			p.writer <- piece

		case MsgHave:
			fmt.Println("[MESSAGE] have")
		case MsgCancel:
			fmt.Println("[MESSAGE] cancel")
		case MsgPort:
			fmt.Println("[MESSAGE] port")
		}
	}
}

func (p *Peer) initiateHandshake(hs *Handshake) error {
	_, err := p.conn.Write(hs.Bytes())
	if err != nil {
		return fmt.Errorf("failed to write handshake: %v", err)
	}

	peerHandshake, err := ReadHandshake(p.conn)
	if err != nil {
		return fmt.Errorf("failed to read handshake: %v", err)
	}

	pp := string(peerHandshake.Pstr)
	hp := string(hs.Pstr)

	if hp != pp {
		return fmt.Errorf("handshake: protocol strings do not match: clients=%s, peers=%s", hp, pp)
	}

	if peerHandshake.InfoHash != hs.InfoHash {
		return fmt.Errorf("handshake: info hashes do not match: %s", p.conn.RemoteAddr())
	}

	return nil
}
