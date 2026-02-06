package torrent

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"
)

type Bitfield []byte

func (b Bitfield) HasPiece(index int) bool {
	byteIndex := index / 8
	offset := index % 8
	return b[byteIndex]>>(7-offset)&1 == 1
}

func (b Bitfield) SetPiece(index int) bool {
	return false
}

type pipeline struct {
	windowSize int
	inflight   int
	nextBlock  int
}

func NewPipeline(windowSize int) *pipeline {
	return &pipeline{windowSize: windowSize}
}

func (p *pipeline) canRequest() bool {
	return p.inflight < p.windowSize
}

func (peer *Peer) dispatchRequests() {
	if !peer.canRequest() {
		return
	}
	if !peer.pipeline.canRequest() {
		return
	}

	for peer.pipeline.inflight < peer.pipeline.windowSize {
		index := *peer.assignedPiece
		begin := peer.pipeline.nextBlock * peer.blockSize
		blockLen := peer.blockSize

		lastPieceID := peer.numOfPieces - 1

		if index == lastPieceID {
			remaining := peer.totalLength - (lastPieceID * peer.pieceLength) - begin
			if remaining < blockLen {
				blockLen = remaining
			}
		}

		peer.sendRequest(index, begin, blockLen)

		peer.pipeline.inflight++
		peer.pipeline.nextBlock++

		if peer.pipeline.nextBlock >= int(peer.numBlocksPerPiece) {
			peer.pipeline.nextBlock = 0
			// TODO: assign next available piece (THAT THIS PEER HAVE AND THAT'S NOT DOWNLOADED OR ASSIGNED by another peer) to the peer
			peer.AssignPiece(index + 1)
		}
	}
}

// Peer edge cases
// 1. Chokes mid piece: need to place assigned piece back to queue and reaasign new piece
// 2. Peer disconnects mid-piece:
// 3. Slow peer - timeouts or reassigning pieces to faster peers, keep track of response time.
// 4. Request pipeline - sliding window algorithm, the peer needs to request blocks from outside of assigned pieces. It needs an ability to assign pieces by itself, need to check for remaining window requests and request for the next available piece. this is problematic cause peer manager assi
type Peer struct {
	ID       int
	conn     net.Conn
	bitfield Bitfield

	amChoking      bool
	amInterested   bool
	peerChoking    bool
	peerInterested bool

	pipeline      *pipeline
	writer        chan<- PieceMessage
	assignedPiece *int

	keepAliveTickInterval time.Duration

	numOfPieces       int
	totalLength       int
	pieceLength       int
	blockSize         int
	numBlocksPerPiece int8

	log *slog.Logger
}

// Closes and clears resources
// connection, keep alive ticker
func (peer *Peer) Close() {}

func (peer *Peer) AssignPiece(pieceIndex int) bool {
	if peer.bitfield != nil && !peer.bitfield.HasPiece(pieceIndex) {
		return false
	}
	peer.log.Debug("[ASSIGN]", "piece", pieceIndex)
	peer.assignedPiece = &pieceIndex
	return true
}

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

func (p *Peer) sendHave() {
	// return p.writeMsg(Message{ID: MsgHave, Payload: []byte()})
}

func (p *Peer) sendRequest(index, begin, block int) error {
	p.log.Debug("[REQUEST]", "piece", index, "begin", begin, "block", block, "inflight", p.pipeline.inflight)
	payload := FormatRequest(index, begin, block)
	return p.writeMsg(Message{ID: MsgRequest, Payload: payload})
}

func (p *Peer) canRequest() bool {
	return p.assignedPiece != nil && p.amInterested && !p.peerChoking
}

func (p *Peer) ReadMessages() error {
	if p.keepAliveTickInterval == 0 {
		p.keepAliveTickInterval = time.Minute
	}

	p.log = slog.With("peer", p.conn.RemoteAddr())

	p.sendInterested()

	ticker := time.NewTicker(p.keepAliveTickInterval)
	defer ticker.Stop()
	go func() {
		for {
			<-ticker.C
			p.sendKeepAlive()
		}
	}()

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
			// reassign piece
			// push piece back to queue
			p.log.Debug("[CHOKE]")
			p.peerChoking = true
		case MsgUnchoke:
			p.log.Debug("[UNCHOKE]")
			p.peerChoking = false
			p.dispatchRequests()
		case MsgInterested:
			p.log.Debug("[INTERESTED]")
			p.peerInterested = true
		case MsgUninterested:
			p.log.Debug("[UNINTERESTED]")
			p.peerInterested = false
		case MsgBitfield:
			p.log.Debug("[BITFIELD]")
			p.bitfield = msg.Payload
		case MsgRequest:
			p.log.Debug("[REQUEST]")
			p.bitfield = msg.Payload
		case MsgPiece:
			piece := ParsePieceMessage(msg.Payload)
			p.pipeline.inflight--
			p.pipeline.inflight--
			p.dispatchRequests()
			p.writer <- piece

			p.log.Debug(
				"[PIECE]",
				"piece", piece.index,
				"begin", piece.begin,
				"len", len(piece.block),
				// "inflight", p.pipeline.inflight,
			)

		case MsgHave:
			p.log.Debug("[REQUEST]")
		case MsgCancel:
			p.log.Debug("[CANCEL]")
		case MsgPort:
			p.log.Debug("[PORT]")
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
