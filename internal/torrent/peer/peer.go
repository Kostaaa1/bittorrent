package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"test/internal/torrent"
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
	ID   uint64
	Addr string

	conn           net.Conn
	bitfield       Bitfield
	amChoking      bool
	amInterested   bool
	peerChoking    bool
	peerInterested bool
	writer         chan<- PieceMessage
	info           *torrent.TorrentInfo
	log            *slog.Logger
	pipeline       *pipeline
	// tm             *timeoutManager
	// maybe use channels instead with scheduler.Type
	OnUnassign     func(pieces []int)
	OnUnchoke      func()
	OnHandshake    func()
	OnMissingPiece func()
}

func New(
	id uint64,
	conn net.Conn,
	info *torrent.TorrentInfo,
	w chan<- PieceMessage,
	log *slog.Logger,
) *Peer {
	return &Peer{
		ID:   id,
		Addr: conn.RemoteAddr().String(),
		info: info,
		conn: conn,
		// tm:           newTimeoutManager(),
		amInterested: false,
		peerChoking:  true,
		writer:       w,
		log:          log,
	}
}

func (peer *Peer) canRequest() bool {
	return peer.amInterested && !peer.peerChoking
}

func (peer *Peer) CanAssign() bool {
	if !peer.canRequest() {
		return false
	}
	return peer.pipeline.CanAssign()
}

func (peer *Peer) HasPiece(piece int) bool {
	if peer.bitfield == nil {
		return true
	}
	return peer.bitfield.HasPiece(piece)
}

func (peer *Peer) Assign(piece int) bool {
	if !peer.CanAssign() {
		return false
	}
	if !peer.HasPiece(piece) {
		return false
	}
	peer.pipeline.assign(piece)
	peer.log.Debug(
		"[ASSIGN]",
		"piece", piece,
		"peer", peer.Addr,
		"assigned", peer.pipeline.assigned,
	)
	return true
}

func (peer *Peer) UnassignPiece(piece int) {
	peer.pipeline.unassign(piece)
	peer.log.Debug("[UNASSIGN]",
		"piece", piece,
		"peer", peer.Addr,
		"assigned", peer.pipeline.assigned,
	)
}

func (peer *Peer) AssignedPieces() []int {
	if peer.pipeline == nil {
		return nil
	}
	return peer.pipeline.assignedPieces()
}

func (peer *Peer) Missing() int {
	return peer.pipeline.missing()
}

func (peer *Peer) Close() error {
	return peer.conn.Close()
}

func (peer *Peer) dispatchRequests(piece, begin int) {
	peer.pipeline.removePending(piece, begin)
	if peer.canRequest() {
		if err := peer.pipeline.dispatch(); err != nil {
			if errors.Is(err, ErrFailedToAssignNext) {
				peer.OnMissingPiece()
			}
		}
	}
}

func (peer *Peer) Open(ctx context.Context, hs Handshake, b Bitfield) error {
	defer peer.conn.Close()

	if err := peer.initiateHandshake(hs); err != nil {
		peer.log.Error("[HANDSHAKE]", "status", "failed", "error", err)
		peer.conn.Close()
		return err
	}

	peer.log.Info("[HANDSHAKE]", "status", "success", "peer", peer.Addr)

	if peer.OnHandshake != nil {
		peer.OnHandshake()
	}

	peer.sendBitfield(b)
	peer.sendInterested()

	peer.pipeline = newPipeline(10, 10, peer.info,
		func(piece, begin, block int) {
			peer.sendRequest(piece, begin, block)
		},
	)

	// go peer.tm.run(ctx, time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		msg, err := readMessage(peer.conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				peer.log.Debug("[SLOW PEER]", "peer", peer.Addr)
				return peer.Close()
			}
			return err
		}

		switch msg.ID {
		case MsgChoke:
			peer.log.Debug("[CHOKE]", "peer", peer.Addr)

			if peer.peerChoking {
				continue
			}

			peer.peerChoking = true

			if len(peer.pipeline.assigned) > 0 {
				// peer.tm.add(MsgChoke, chokeTimeout, func() {
				// 	peer.log.Debug("CHOKE TIMEOUT EXCEEDED!!!!!!!!!")
				// 	peer.OnUnassign(peer.pipeline.drain())
				// })
			}

		case MsgUnchoke:
			peer.log.Debug("[UNCHOKE]", "peer", peer.Addr)
			peer.peerChoking = false
			// peer.tm.cancel(MsgChoke)
			peer.OnUnchoke()
			if peer.canRequest() {
				if err := peer.pipeline.dispatch(); err != nil {
					if errors.Is(err, ErrFailedToAssignNext) {
						peer.OnMissingPiece()
					}
				}
			}
		case MsgInterested:
			peer.log.Debug("[INTERESTED]", "peer", peer.Addr)
			peer.peerInterested = true
			peer.sendUnchoke()
		case MsgUninterested:
			peer.log.Debug("[UNINTERESTED]", "peer", peer.Addr)
			peer.peerInterested = false
		case MsgBitfield:
			peer.log.Debug("[BITFIELD]", "peer", peer.Addr)
			peer.bitfield = msg.Payload
		case MsgRequest:
			peer.log.Debug("[REQUEST]", "peer", peer.Addr)
		case MsgPiece:
			piece := parsePieceMessage(msg.Payload)
			peer.log.Debug(
				"[PIECE]",
				"piece", piece.Index,
				"begin", piece.Begin,
				"peer", peer.Addr,
			)
			peer.writer <- piece
			peer.dispatchRequests(piece.Index, piece.Begin)
		case MsgHave:
			peer.log.Debug("[REQUEST]", "peer", peer.Addr)
		case MsgCancel:
			peer.log.Debug("[CANCEL]", "peer", peer.Addr)
		case MsgPort:
			peer.log.Debug("[PORT]", "peer", peer.Addr)
		}
	}
}

func (peer *Peer) initiateHandshake(hs Handshake) error {
	_, err := peer.conn.Write(hs.Bytes())
	if err != nil {
		return fmt.Errorf("failed handshake: write: %v", err)
	}

	peerHandshake, err := ReadHandshake(peer.conn)
	if err != nil {
		return fmt.Errorf("failed handhskae: read: %v", err)
	}

	pp := string(peerHandshake.Pstr)
	hp := string(hs.Pstr)

	if hp != pp {
		return fmt.Errorf("failed handshake: protocol strings do not match: clients=%s, peers=%s", hp, pp)
	}

	if peerHandshake.InfoHash != hs.InfoHash {
		return fmt.Errorf("failed handshake: info hashes do not match: %s", peer.Addr)
	}

	return nil
}

// func (peer *Peer) Print() {
// 	if peer.pipeline != nil {
// 		peer.log.Info(
// 			"[PEER]",
// 			"addr", peer.Addr,
// 			"id", peer.ID,
// 			"pipeline.pending", len(peer.pipeline.pending),
// 			"pipeline.active", peer.pipeline.active,
// 			"pipeline.len_assigned_pieces", len(peer.pipeline.assigned),
// 			"pipeline.assigned_pieces", peer.pipeline.assigned,
// 		)
// 	} else {
// 		peer.log.Info(
// 			"[PEER]",
// 			"addr", peer.Addr,
// 			"id", peer.ID,
// 		)
// 	}
// }
