package peer

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
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

func (peer *Peer) canRequest() bool {
	return peer.amInterested && !peer.peerChoking
}

func (peer *Peer) dispatchRequests() {
	if !peer.canRequest() {
		return
	}

	if peer.pieceQueue.curr == nil {
		peer.assignNext()
	}

	for peer.inflight < peer.windowSize {
		index := *peer.pieceQueue.curr
		begin := peer.nextBlock * peer.info.blockSize
		blockLen := peer.info.blockSize

		lastPieceID := peer.info.numOfPieces - 1

		if index == lastPieceID {
			remaining := peer.info.totalLength - (lastPieceID * peer.info.pieceLength) - begin
			if remaining < blockLen {
				blockLen = remaining
			}
		}

		peer.sendRequest(index, begin, blockLen)

		peer.inflight++
		peer.nextBlock++

		if peer.nextBlock >= int(peer.info.numBlocksPerPiece) {
			peer.nextBlock = 0
			peer.assignNext()

			// TODO: assign next available piece (THAT THIS PEER HAVE AND THAT'S NOT DOWNLOADED OR ASSIGNED by another peer) to the peer
			// if index+1 < peer.info.numOfPieces {
			// peer.assignPiece(index + 1)
			// 	peer.assignNext()
			// }
		}
	}
}

type torrentInfo struct {
	numOfPieces       int
	totalLength       int
	pieceLength       int
	blockSize         int
	numBlocksPerPiece int8
}

// Peer edge cases
// 1. Chokes mid piece: need to place assigned piece back to queue and reaasign new piece
// 2. Peer disconnects mid-piece:
// 3. Slow peer - timeouts or reassigning pieces to faster peers, keep track of response time.
// 4. Request pipeline - sliding window algorithm, the peer needs to request blocks from outside of assigned pieces. It needs an ability to assign pieces by itself, need to check for remaining window requests and request for the next available piece. this is problematic cause peer manager assi
type Peer struct {
	ID       uint64
	conn     net.Conn
	bitfield Bitfield

	amChoking      bool
	amInterested   bool
	peerChoking    bool
	peerInterested bool

	writer chan<- PieceMessage

	keepAliveTickInterval time.Duration

	info torrentInfo
	log  *slog.Logger

	// Request Pipeline
	windowSize int
	inflight   int
	nextBlock  int

	pieceQueue *pieceQueue
}

type pieceQueue struct {
	mu      sync.Mutex
	maxSize int
	queue   map[int]bool
	curr    *int
}

func (pq *pieceQueue) canAssign() bool {
	return len(pq.queue) < pq.maxSize
}

func (peer *Peer) CanAssign() bool {
	return peer.pieceQueue.canAssign()
}

func New(
	id uint64,
	conn net.Conn,
	writer chan<- PieceMessage,
	log *slog.Logger,
) *Peer {
	queue := &pieceQueue{
		maxSize: 5,
		queue:   make(map[int]bool),
		curr:    nil,
	}

	return &Peer{
		ID:           id,
		conn:         conn,
		amInterested: false,
		peerChoking:  true,
		writer:       writer,
		log:          log,
		windowSize:   5,
		pieceQueue:   queue,
	}
}

func (p *Peer) SetInfo(
	numOfPieces int,
	totalLength int,
	pieceLength int,
	blockSize int,
	numBlocksPerPiece int8,
) {
	p.info = torrentInfo{
		numOfPieces,
		totalLength,
		pieceLength,
		blockSize,
		numBlocksPerPiece,
	}
}

func (p *Peer) HasPiece(pieceIndex int) bool {
	if p.bitfield == nil {
		return true
	}
	return p.bitfield.HasPiece(pieceIndex)
}

func (peer *Peer) assignNext() {
	peer.pieceQueue.mu.Lock()
	defer peer.pieceQueue.mu.Unlock()

	c := peer.pieceQueue.curr
	if c != nil {
		delete(peer.pieceQueue.queue, *c)
	}

	for piece := range peer.pieceQueue.queue {
		peer.pieceQueue.curr = &piece
		break
	}
}

func (peer *Peer) AddPieceToQueue(pieceID int) {
	peer.pieceQueue.mu.Lock()
	defer peer.pieceQueue.mu.Unlock()

	peer.log.Debug("[ASSIGN]", "piece", pieceID, "peer", peer.conn.RemoteAddr())
	if peer.pieceQueue.curr != nil {
		delete(peer.pieceQueue.queue, *peer.pieceQueue.curr)
	}
	peer.pieceQueue.queue[pieceID] = true
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
	p.log.Debug("[REQUEST]", "piece", index, "begin", begin, "block", block)
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

			p.inflight--
			p.dispatchRequests()
			p.writer <- piece

			p.log.Debug(
				"[PIECE]",
				"piece", piece.Index,
				"begin", piece.Begin,
				"len", len(piece.Block),
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
