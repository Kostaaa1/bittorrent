package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent"
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
	ID             uint64
	Addr           string
	conn           net.Conn
	bitfield       Bitfield
	amChoking      bool
	amInterested   bool
	peerChoking    bool
	peerInterested bool
	writer         chan<- PieceMessage
	info           *torrent.TorrentInfo
	log            *logger.Log
	pipeline       *pipeline
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
	log *logger.Log,
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

func (peer *Peer) Assignable() bool {
	if !peer.canRequest() {
		return false
	}
	return peer.pipeline.assignable()
}

func (peer *Peer) Missing() int {
	return peer.pipeline.missing()
}

func (peer *Peer) CanAssignPiece(piece int) bool {
	if !peer.canRequest() {
		return false
	}
	if !peer.hasPiece(piece) {
		return false
	}
	return true
}

func (peer *Peer) hasPiece(piece int) bool {
	if peer.bitfield == nil {
		return true
	}
	return peer.bitfield.HasPiece(piece)
}

func (peer *Peer) UnassignPiece(piece int) {
	peer.pipeline.unassign(piece)
	peer.log.Assignment("[UNASSIGN SINGLE PIECE]",
		"piece", piece,
		"peer", peer.Addr,
		"assigned", peer.pipeline.assigned,
	)
}

func (peer *Peer) Print() {
	peer.log.Info("[PEER]",
		"addr", peer.Addr,
		"choked", peer.peerChoking,
		"interested", peer.amInterested,
		"active", peer.pipeline.active,
		"pieces", peer.pipeline.assigned,
		"pieces_len", len(peer.pipeline.assigned),
		"inflight", peer.pipeline.pending,
		"len_inflight", len(peer.pipeline.pending),
	)
}

func (peer *Peer) Unassign(pieces []int) {
	for _, piece := range pieces {
		peer.pipeline.unassign(piece)
	}
	peer.log.Assignment("[UNASSIGN PIECES]",
		"pieces", pieces,
		"peer", peer.Addr,
		"assigned", peer.pipeline.assigned,
	)
}

func (peer *Peer) Assign(pieces []int) {
	for _, piece := range pieces {
		if ok := peer.pipeline.assign(piece); !ok {
			panic("IT NEEDS TO ASSIGN THE PIECE")
		}
	}
	peer.log.Assignment("[ASSIGNED]",
		"new_pieces", pieces,
		"peer", peer.Addr,
		"assigned", peer.pipeline.assigned,
	)
}

// func (peer *Peer) Reassign() []int {
// 	return peer.pipeline.reassign()
// }

func (peer *Peer) Assigned() []int {
	if peer.pipeline == nil {
		return nil
	}
	return peer.pipeline.Assigned()
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

	peer.log.Traffic("[HANDSHAKE]", "status", "success", "peer", peer.Addr)

	if peer.OnHandshake != nil {
		peer.OnHandshake()
	}

	peer.sendBitfield(b)
	peer.sendInterested()

	peer.pipeline = newPipeline(
		peer.Addr,
		10,
		10,
		peer.info,
		func(piece, begin, block int) {
			peer.sendRequest(piece, begin, block)
		},
		peer.log,
	)

	var chokeDeadlineFn *time.Timer

	for {
		select {
		case <-ctx.Done():
			return nil

		default:
			msg, err := readMessage(peer.conn)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
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

				chokeDeadlineFn = time.AfterFunc(time.Second*15, func() {
					fmt.Println("UNCHOKE EXCEED", peer.Addr, msg)
					pieces := peer.pipeline.reset()
					peer.OnUnassign(pieces)
				})
			case MsgUnchoke:
				peer.log.Debug("[UNCHOKE]", "peer", peer.Addr)

				peer.peerChoking = false
				if chokeDeadlineFn != nil {
					chokeDeadlineFn.Stop()
					chokeDeadlineFn = nil
				}

				if peer.canRequest() {
					peer.OnUnchoke()
					if err := peer.pipeline.dispatch(); err != nil {
						if errors.Is(err, ErrFailedToAssignNext) {
							peer.OnMissingPiece()
						}
					}
				}

			case MsgPiece:
				piece := parsePieceMessage(msg.Payload)
				peer.log.Traffic(
					"[PIECE]",
					"piece", piece.Index,
					"begin", piece.Begin,
					"peer", peer.Addr,
				)
				peer.writer <- piece
				peer.dispatchRequests(piece.Index, piece.Begin)
			case MsgInterested:
				peer.log.Traffic("[INTERESTED]", "peer", peer.Addr)
				peer.peerInterested = true
				peer.sendUnchoke()
			case MsgUninterested:
				peer.log.Traffic("[UNINTERESTED]", "peer", peer.Addr)
				peer.peerInterested = false
			case MsgBitfield:
				peer.log.Traffic("[BITFIELD]", "peer", peer.Addr)
				peer.bitfield = msg.Payload
			case MsgRequest:
				peer.log.Traffic("[REQUEST]", "peer", peer.Addr)
			case MsgHave:
				peer.log.Traffic("[HAVE]", "peer", peer.Addr)
			case MsgCancel:
				peer.log.Traffic("[CANCEL]", "peer", peer.Addr)
			case MsgPort:
				peer.log.Traffic("[PORT]", "peer", peer.Addr)
			}
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
