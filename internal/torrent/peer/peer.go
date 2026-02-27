package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"test/internal/torrent"
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
}

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
	info                  *torrent.TorrentInfo
	log                   *slog.Logger
	pipeline              *pipeline
	OnUnassign            func(pieces []int)
	OnUnchoke             func()
	OnHandshake           func()
	OnMissingPiece        func()
}

func New(
	id uint64,
	conn net.Conn,
	info *torrent.TorrentInfo,
	w chan<- PieceMessage,
	log *slog.Logger,
) *Peer {
	return &Peer{
		ID:           id,
		Addr:         conn.RemoteAddr().String(),
		info:         info,
		conn:         conn,
		amInterested: false,
		peerChoking:  true,
		writer:       w,
		log:          log,
	}
}

func (peer *Peer) canRequest() bool {
	return peer.amInterested && !peer.peerChoking
}

var ErrFailedToassignNext = errors.New("failed to assign new piece")

func (peer *Peer) dispatchRequests() error {
	if !peer.canRequest() {
		return nil
	}

	if peer.pipeline.inflight > 0 {
		peer.pipeline.inflight--
	}

	for peer.pipeline.inflight < peer.pipeline.windowSize {
		index, ok := peer.pipeline.getActiveOrAssignNext()
		if !ok {
			// TODO: ask scheduler for new piece. split peer assiGned to half and assign then to this one
			return ErrFailedToassignNext
		}

		block := peer.info.BlockSize
		numOfPieces := peer.info.NumOfPieces

		lastPieceID := numOfPieces - 1
		blocksForPiece := peer.info.NumBlocksPerPiece

		if index == lastPieceID {
			size := peer.info.TotalLength - (lastPieceID * peer.info.PieceLength)
			blocksForPiece = int(math.Ceil(float64(size) / float64(block)))
		}

		if peer.pipeline.nextBlock >= blocksForPiece {
			index, ok = peer.pipeline.assignNext()
			if !ok {
				return ErrFailedToassignNext
			}
			peer.log.Debug("[REASSIGNED]", "new_piece_for_requesting", index)
		}

		begin := peer.pipeline.nextBlock * block

		if index == lastPieceID {
			size := peer.info.TotalLength - (lastPieceID * peer.info.PieceLength)
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

	return nil
}

func (peer *Peer) CanAssign() bool {
	if !peer.canRequest() {
		return false
	}
	return peer.pipeline.CanAssign()
}

func (p *Peer) HasPiece(pieceID int) bool {
	if p.bitfield == nil {
		return true
	}
	return p.bitfield.HasPiece(pieceID)
}

func (peer *Peer) AddPieceToQueue(pieceID int) {
	peer.log.Debug("[ASSIGN]", "piece", pieceID)
	peer.pipeline.assign(pieceID)
}

func (peer *Peer) UnassignPiece(pieceID int) {
	peer.log.Debug("[UNASSIGN]", "piece_index", pieceID)
	peer.pipeline.unassign(pieceID)
}

func (peer *Peer) AssignedPieces() []int {
	if peer.pipeline == nil {
		return nil
	}
	return peer.pipeline.assignedPieces()
}

func (peer *Peer) ReassignPieces() []int {
	return peer.pipeline.reassignPieces()
}

func (p *Peer) Close() error {
	return p.conn.Close()
}

func (p *Peer) Open(ctx context.Context, hs Handshake, b Bitfield) error {
	defer p.conn.Close()

	p.log = slog.With("peer", p.conn.RemoteAddr())

	if err := p.initiateHandshake(hs); err != nil {
		p.log.Error("[HANDSHAKE]", "status", "failed", "error", err)
		p.conn.Close()
		return err
	}

	p.log.Info("[HANDSHAKE]", "status", "success")
	if p.OnHandshake != nil {
		p.OnHandshake()
	}

	read := false
	p.conn.SetReadDeadline(time.Now().Add(time.Second * 45))

	p.sendBitfield(b)
	p.sendInterested()

	// if p.keepAliveTickInterval == 0 {
	// 	p.keepAliveTickInterval = time.Minute
	// }
	// ticker := time.NewTicker(p.keepAliveTickInterval)
	// defer ticker.Stop()
	// go func() {
	// 	for range ticker.C {
	// 		p.sendKeepAlive()
	// 	}
	// }()

	p.pipeline = newPipeline(10, 10)

	var cancelFunc context.CancelFunc

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		msg, err := readMessage(p.conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				p.log.Debug("[SLOW PEER]")
				return p.Close()
			}
			return err
		}

		if !read {
			p.conn.SetReadDeadline(time.Time{})
			read = true
		}

		switch msg.ID {
		case MsgChoke:
			p.log.Debug("[CHOKE]")
			if p.peerChoking {
				continue
			}

			p.peerChoking = true

			if len(p.pipeline.assigned) > 0 {
				ctx, cancel := context.WithCancel(context.Background())
				cancelFunc = cancel

				go func() {
					for {
						select {
						case <-ctx.Done():
							p.log.Debug("UNCHOKED, canceled")
							return
						case <-time.After(time.Second * 30):
							p.log.Debug("TIME AFTER")
							pieces := p.AssignedPieces()
							p.pipeline.active = nil
							p.pipeline.assigned = nil
							p.pipeline.inflight = 0
							p.pipeline.nextBlock = 0
							p.pipeline.queue = nil
							p.OnUnassign(pieces)
							return
						}
					}
				}()
			}
		case MsgUnchoke:
			p.log.Debug("[UNCHOKE]")
			p.peerChoking = false
			if cancelFunc != nil {
				p.log.Debug("CANCELING CHOKE TIMEOUT")
				cancelFunc()
			}
			p.OnUnchoke()
			if err := p.dispatchRequests(); err != nil {
				if errors.Is(err, ErrFailedToassignNext) {
					p.OnMissingPiece()
				}
			}
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
			piece := parsePieceMessage(msg.Payload)
			p.log.Debug(
				"[PIECE]",
				"piece", piece.Index,
				"begin", piece.Begin,
				"len", len(piece.Block),
			)
			p.writer <- piece
			p.dispatchRequests()
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
		return fmt.Errorf("handshake: info hashes do not match: %s")
	}

	return nil
}

func (p *Peer) Print() {
	if p.pipeline != nil {
		p.log.Info(
			"[PEER]",
			"addr", p.Addr,
			"id", p.ID,
			"pipeline.inflight", p.pipeline.inflight,
			"pipeline.active", p.pipeline.active,
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
