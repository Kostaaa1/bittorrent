package peer

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
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

func (peer *Peer) canRequest() bool {
	return peer.amInterested && !peer.peerChoking
}

func (peer *Peer) UnassignPiece(pieceID int) {
	if peer.pipeline != nil {
		peer.log.Debug("[UNASSIGN]", "piece_index", pieceID)
		peer.pipeline.unassignPiece(pieceID)
		if peer.pipeline.active != nil && *peer.pipeline.active == pieceID {
			peer.pipeline.active = nil
		}
	}
}

func (peer *Peer) dispatchRequests() {
	if peer.pipeline == nil {
		return
	}

	if !peer.canRequest() {
		return
	}

	if peer.pipeline.inflight > 0 {
		peer.pipeline.inflight--
	}

	for peer.pipeline.inflight < peer.pipeline.windowSize {
		index, ok := peer.pipeline.getActiveOrAssignNext()
		if !ok {
			fmt.Println("failed to get current or assign next")
			return
		}

		block := peer.info.blockSize
		numOfPieces := peer.info.numOfPieces

		lastPieceID := numOfPieces - 1
		blocksForPiece := peer.info.numBlocksPerPiece

		if index == lastPieceID {
			size := peer.info.totalLength - (lastPieceID * peer.info.pieceLength)
			blocksForPiece = int(math.Ceil(float64(size) / float64(block)))
		}

		if peer.pipeline.nextBlock >= blocksForPiece {
			index, ok = peer.pipeline.assignNext()
			if !ok {
				fmt.Println("failed to get current or assign next")
				return
			}
			peer.log.Debug("[REASSIGNED]", "new_piece_for_requesting", index)
		}

		begin := peer.pipeline.nextBlock * block

		if index == lastPieceID {
			size := peer.info.totalLength - (lastPieceID * peer.info.pieceLength)
			remaining := size - begin
			if remaining < 0 {
				panic("TODO: REMAINING IS LESS THEN 0")
			}
			if remaining < block {
				block = remaining
			}
		}

		peer.sendRequest(index, begin, block)
		peer.pipeline.inflight++
		peer.pipeline.nextBlock++
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
	OnHandshake           func()
}

func (peer *Peer) CanAssign() bool {
	if peer.pipeline == nil {
		return false
	}
	return peer.pipeline.CanAssign()
}

func New(
	id uint64,
	conn net.Conn,
	w chan<- PieceMessage,
	log *slog.Logger,
) *Peer {
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

func (p *Peer) HasPiece(pieceID int) bool {
	if p.bitfield == nil {
		return true
	}
	return p.bitfield.HasPiece(pieceID)
}

func (peer *Peer) AddPieceToQueue(pieceID int) {
	peer.log.Debug("[ASSIGN]", "piece", pieceID, "peer", peer.conn.RemoteAddr())
	peer.pipeline.addPiece(pieceID)
}

func (p *Peer) writeMsg(msg Message) error {
	// p.log.Debug("[WRITE]", "tag", msg.String())
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
	fmt.Println("SENDING BITFIELD:", bf)
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
		"begin", begin,
		"block", block,
	)
	msg := make([]byte, 12)
	binary.BigEndian.PutUint32(msg[:4], uint32(index))
	binary.BigEndian.PutUint32(msg[4:8], uint32(begin))
	binary.BigEndian.PutUint32(msg[8:12], uint32(block))
	return p.writeMsg(Message{ID: MsgRequest, Payload: msg})
}

func (peer *Peer) AssignedPieces() []int {
	if peer.pipeline == nil {
		return nil
	}
	return peer.pipeline.assignedPieces()
}

func (p *Peer) Open(ctx context.Context, hs Handshake, b Bitfield) error {
	defer p.conn.Close()

	p.log = slog.With("peer", p.conn.RemoteAddr())

	if err := p.initiateHandshake(hs); err != nil {
		p.log.Error("[HANDSHAKE]", "status", "failed", "error", err)
		p.conn.Close()
		return err
	}

	p.log.Info("[HANDSHAKE]", "status", "success", "peer", p.conn.RemoteAddr())
	if p.OnHandshake != nil {
		p.OnHandshake()
	}
	if p.keepAliveTickInterval == 0 {
		p.keepAliveTickInterval = time.Minute
	}

	p.sendBitfield(b)
	p.sendInterested()

	ticker := time.NewTicker(p.keepAliveTickInterval)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			p.sendKeepAlive()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		msg, err := ReadMessage(p.conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				p.conn.SetReadDeadline(time.Time{})
				p.log.Debug("[SLOW PEER]")

				pieces := p.AssignedPieces()
				p.OnChoke(pieces)
				p.pipeline = nil
				p.amInterested = false
				p.sendUninterested()
			}
			return err
		}

		switch msg.ID {
		case MsgChoke:
			p.log.Debug("[CHOKE]")
			p.peerChoking = true
			pieces := p.AssignedPieces()
			p.OnChoke(pieces)
			p.pipeline = nil
		case MsgUnchoke:
			p.log.Debug("[UNCHOKE]")
			p.peerChoking = false
			p.pipeline = newPipeline()
			p.OnUnchoke()
			p.dispatchRequests()
			p.conn.SetReadDeadline(time.Now().Add(time.Second * 10))
		case MsgInterested:
			p.log.Debug("[INTERESTED]")
			p.peerInterested = true
			p.sendUnchoke()
		case MsgUninterested:
			p.log.Debug("[UNINTERESTED]")
			p.peerInterested = false
		case MsgBitfield:
			p.log.Debug("[BITFIELD]")
			p.bitfield = msg.Payload
		case MsgRequest:
			p.log.Debug("[REQUEST]")
		case MsgPiece:
			piece := ParsePieceMessage(msg.Payload)
			p.log.Debug(
				"[PIECE]",
				"piece", piece.Index,
				"begin", piece.Begin,
				"len", len(piece.Block),
			)
			p.writer <- piece
			if p.pipeline != nil {
				p.dispatchRequests()
			}

			p.conn.SetReadDeadline(time.Now().Add(time.Second * 10))
		case MsgHave:
			p.log.Debug("[REQUEST]")
		case MsgCancel:
			p.log.Debug("[CANCEL]")
		case MsgPort:
			p.log.Debug("[PORT]")
		}
	}
}

func (p *Peer) Print() {
	if p.pipeline != nil {
		p.log.Info(
			"[PEER]",
			"addr", p.Addr,
			"id", p.ID,
			"pipeline.inflight", p.pipeline.inflight,
			"pipeline.active", p.pipeline.active,
			"pipeline.queue_len", len(p.pipeline.queue),
			"pipeline.len_assigned_pieces", len(p.pipeline.assigned),
			"pipeline.assigned_pieces", p.pipeline.assigned,
		)
	} else {
		p.log.Info(
			"[PEER]",
			"addr", p.Addr,
			"id", p.ID,
		)
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
