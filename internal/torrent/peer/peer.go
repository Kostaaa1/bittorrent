package peer

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

func (b Bitfield) SetPiece(index int) {
	byteIndex := index / 8
	offset := index % 8
	b[byteIndex] |= 1 << (7 - offset)
	// b[byteIndex] ^= 1 << (7 - offset)
}

func (peer *Peer) CanRequest() bool {
	return peer.amInterested && !peer.peerChoking
}

func (peer *Peer) Print() {
	peer.log.Info("[PEER INFO]",
		"addr", peer.Addr,
		"can_request", peer.CanRequest(),
	)
	if peer.pipeline != nil {
		fmt.Println(
			"pipeline.assigned", len(peer.pipeline.pieces),
			"pipeline.curr", peer.pipeline.curr,
			"pipeline.inflight", peer.pipeline.inflight,
			"pipeline.windowSize", peer.pipeline.windowSize,
			"pipeline.nextBlock", peer.pipeline.nextBlock,
		)
	}
}

func (peer *Peer) dispatchRequests() {
	if !peer.CanRequest() {
		return
	}

	if peer.pipeline.curr == nil {
		if assigned := peer.pipeline.assignNext(); !assigned {
			return
		}
	}

	for peer.pipeline.inflight < peer.pipeline.windowSize {
		if peer.pipeline.curr == nil {
			return
		}

		index := *peer.pipeline.curr
		begin := peer.pipeline.nextBlock * peer.info.blockSize
		blockLen := peer.info.blockSize

		lastPieceID := peer.info.numOfPieces - 1

		if index == lastPieceID {
			remaining := peer.info.totalLength - (lastPieceID * peer.info.pieceLength) - begin
			if remaining < blockLen {
				blockLen = remaining
			}
		}

		peer.sendRequest(index, begin, blockLen)

		if peer.pipeline.inflight+1 <= peer.pipeline.windowSize {
			peer.pipeline.inflight++
			peer.pipeline.nextBlock++
		}

		if peer.pipeline.nextBlock >= peer.info.numBlocksPerPiece {
			peer.pipeline.nextBlock = 0
			if assigned := peer.pipeline.assignNext(); !assigned {
				return
			}
		}
	}
}

type torrentInfo struct {
	numOfPieces       int
	totalLength       int
	pieceLength       int
	blockSize         int
	numBlocksPerPiece int
}

func (p *Peer) SetInfo(
	numOfPieces int,
	totalLength int,
	pieceLength int,
	blockSize int,
	numBlocksPerPiece int,
) {
	p.info = torrentInfo{
		numOfPieces,
		totalLength,
		pieceLength,
		blockSize,
		numBlocksPerPiece,
	}
}

// Peer edge cases
// 1. Chokes mid piece: need to place assigned piece back to queue and reaasign new piece
// 2. Peer disconnects mid-piece:
// 3. Slow peer - timeouts or reassigning pieces to faster peers, keep track of response time.
// 4. Request pipeline - sliding window algorithm, the peer needs to request blocks from outside of assigned pieces. It needs an ability to assign pieces by itself, need to check for remaining window requests and request for the next available piece. this is problematic cause peer manager assi
type Peer struct {
	ID                    uint64
	Addr                  string
	conn                  net.Conn
	bitfield              Bitfield
	amChoking             bool
	amInterested          bool
	peerChoking           bool
	peerInterested        bool
	writer                chan<- PieceMessage
	keepAliveTickInterval time.Duration
	info                  torrentInfo
	log                   *slog.Logger
	pipeline              *pipeline
	OnChoke               func(pieces []int)
	OnUnchoke             func()
}

func (peer *Peer) CanAssign() bool {
	return peer.pipeline.canAssign()
}

func New(id uint64, conn net.Conn, w chan<- PieceMessage, log *slog.Logger) *Peer {
	return &Peer{
		ID:           id,
		Addr:         conn.RemoteAddr().String(),
		conn:         conn,
		amInterested: false,
		peerChoking:  true,
		writer:       w,
		log:          log,
	}
}

func (p *Peer) HasPiece(pieceIndex int) bool {
	if p.bitfield == nil {
		return true
	}
	return p.bitfield.HasPiece(pieceIndex)
}

func (peer *Peer) AddPieceToQueue(pieceID int) {
	peer.log.Debug("[ASSIGN]", "piece", pieceID, "peer", peer.conn.RemoteAddr())
	peer.pipeline.addQueue(pieceID)
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
	p.log.Debug("[REQUEST]",
		"piece", index,
		"begin", begin,
		"block", block,
	)
	payload := FormatRequest(index, begin, block)
	return p.writeMsg(Message{ID: MsgRequest, Payload: payload})
}

func (peer *Peer) Close() error {
	return peer.conn.Close()
}

func (p *Peer) Open(hs Handshake) error {
	p.log = slog.With("peer", p.conn.RemoteAddr())

	if err := p.initiateHandshake(hs); err != nil {
		p.log.Error("[HANDSHAKE]", "status", "failed", "error", err)
		return p.Close()
	}

	p.log.Info("[HANDSHAKE]", "status", "success", "peer", p.conn.RemoteAddr())

	if p.keepAliveTickInterval == 0 {
		p.keepAliveTickInterval = time.Minute
	}

	p.sendInterested()

	ticker := time.NewTicker(p.keepAliveTickInterval)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
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
			p.log.Debug("[CHOKE]")

			p.peerChoking = true
			if toReassign := p.pipeline.destroy(); len(toReassign) > 0 {
				p.OnChoke(toReassign)
			}

			// if p.pipeline != nil {
			// 	p.pipeline.mu.Lock()
			// 	left := make([]int, 0)
			// 	for piece := range p.pipeline.pieces {
			// 		left = append(left, piece)
			// 	}
			// 	p.pipeline.mu.Unlock()
			// 	p.pipeline = nil
			// 	p.OnChoke(left)
			// }
			// p.OnChoke(left)

		case MsgUnchoke:
			p.log.Debug("[UNCHOKE]")

			p.peerChoking = false
			p.pipeline = newPipeline()
			p.OnUnchoke()
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
			p.writer <- piece

			p.log.Debug(
				"[PIECE]",
				"piece", piece.Index,
				"begin", piece.Begin,
				"len", len(piece.Block),
			)

			if p.pipeline != nil {
				if p.pipeline.inflight > 0 {
					p.pipeline.inflight--
				}
				p.dispatchRequests()
			}

		case MsgHave:
			p.log.Debug("[REQUEST]")
		case MsgCancel:
			p.log.Debug("[CANCEL]")
		case MsgPort:
			p.log.Debug("[PORT]")
		}
	}
}

func (p *Peer) initiateHandshake(hs Handshake) error {
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

// 817efb333db92ec0ef8042a184da8c039bdd1280de5bedfaf69ad1b431a5c72f  /Users/kostaarsic/Downloads/Guha Rehan - Machine Learning Interview Guide - 2025/Guha Rehan - Machine Learning Interview Guide - 2025.pdf

// 67bb4502dbc99b8b44d5637cd6c995040ad3748957f4ea2ef7f5981f0ae44e2b  /Users/kostaarsic/Downloads/Guha Rehan - Machine Learning Interview Guide - 2025/Guha Rehan - Machine Learning Interview Guide - 2025.epub
