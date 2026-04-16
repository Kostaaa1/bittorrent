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
	"github.com/Kostaaa1/bittorrent/internal/torrent/shared"
)

type Peer struct {
	Addr           string
	conn           net.Conn
	bitfield       shared.Bitfield
	amChoking      bool
	amInterested   bool
	peerChoking    bool
	peerInterested bool
	writer         chan<- PieceMessage
	info           *torrent.TorrentInfo
	log            *logger.Log
	pipeline       *pipeline

	// add peer to peer pool
	// OnHandshake func()
	// all pieces that needs to be reassigned
	// OnReassign func(pieces []int)
	// signal scheduler that we can accept pieces from it
	// OnScehdulePieces func()
	// peer has no pieces, e
	// OnMissingPiece func()
}

func New(
	conn net.Conn,
	info *torrent.TorrentInfo,
	w chan<- PieceMessage,
	log *logger.Log,
) *Peer {
	return &Peer{
		Addr:         conn.RemoteAddr().String(),
		info:         info,
		conn:         conn,
		amInterested: false,
		peerChoking:  true,
		writer:       w,
		log:          log,
	}
}

// TODO: function for ticking and eliminating pending requests, keep track  amount of times it reassigned the piece with this method. If it happened many times, unassign all pieces from it (but keep unchoked and interested)

func (peer *Peer) CanAssign() bool {
	if !peer.canRequest() {
		return false
	}
	return peer.pipeline.CanAssign()
}

func (peer *Peer) UnassignPiece(piece int) {
	peer.pipeline.unassign(piece)
	peer.log.Assignment("[UNASSIGN SINGLE PIECE]",
		"piece", piece,
		"peer", peer.Addr,
		"assigned", peer.pipeline.pieces,
	)
}

func (peer *Peer) Unassign(pieces []int) {
	for _, piece := range pieces {
		peer.pipeline.unassign(piece)
	}
	peer.log.Assignment("[UNASSIGN PIECES]",
		"pieces", pieces,
		"peer", peer.Addr,
		"assigned", peer.pipeline.pieces,
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
		"assigned", peer.pipeline.pieces,
	)
}

func (peer *Peer) Assigned() []int {
	if peer.pipeline == nil {
		return nil
	}
	return peer.pipeline.Assigned()
}

func (peer *Peer) Capacity() int {
	return peer.pipeline.Capacity()
}

func (peer *Peer) canRequest() bool {
	return peer.amInterested && !peer.peerChoking
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

func (peer *Peer) Close() error {
	return peer.conn.Close()
}

func (peer *Peer) Open(ctx context.Context, hs Handshake, b shared.Bitfield) error {
	defer peer.conn.Close()

	if err := peer.initiateHandshake(hs); err != nil {
		peer.log.Error("[HANDSHAKE]", "status", "failed", "error", err)
		peer.conn.Close()
		return err
	}

	peer.log.Traffic("[HANDSHAKE]", "status", "success", "peer", peer.Addr)

	// if peer.OnHandshake != nil {
	// 	peer.OnHandshake()
	// }

	// TODO: add keepalive
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
				}
				return err
			}

			switch msg.ID {
			case MsgChoke:
				peer.log.Debug("[CHOKE]",
					"peer", peer.Addr,
					"pieces", peer.pipeline.pieces,
					// "pending", peer.pipeline.pending,
					"active", peer.pipeline.active,
				)

				if peer.peerChoking {
					continue
				}

				peer.peerChoking = true

				chokeDeadlineFn = time.AfterFunc(time.Second*15, func() {
					pieces := peer.pipeline.reset()
					peer.log.Debug("[UNCHOKE DEADLINE EXCEED - reasignign pieces]",
						"peer", peer.Addr,
						"pieces", pieces,
						// "pending", peer.pipeline.pending,
						"active", peer.pipeline.active,
					)
					// peer.OnReassign(pieces)
				})
			case MsgUnchoke:
				peer.peerChoking = false
				if chokeDeadlineFn != nil {
					chokeDeadlineFn.Stop()
					chokeDeadlineFn = nil
				}

				peer.log.Debug("[UNCHOKE - removing deadling func]",
					"peer", peer.Addr,
					"can_request", peer.canRequest(),
					"can_dispatch", peer.pipeline.canDispatch(),
					"pieces", peer.pipeline.pieces,
					// "pending", peer.pipeline.pending,
					"active", peer.pipeline.active,
				)

				// peer.OnScehdulePieces()
				if peer.canRequest() {
					if err := peer.pipeline.Dispatch(); err != nil {
						if errors.Is(err, ErrFailedToAssignNext) {
							// peer.OnScehdulePieces()
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
				peer.pipeline.removePendingBlock(piece.Index, piece.Begin)
				if peer.canRequest() {
					if err := peer.pipeline.Dispatch(); err != nil {
						if errors.Is(err, ErrFailedToAssignNext) {
							// peer.OnScehdulePieces()
						}
					}
				}
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
		return fmt.Errorf("failed handshake: read: %v", err)
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

func (peer *Peer) Print() {
	peer.log.Info("[PEER]",
		"addr", peer.Addr,
		"choked", peer.peerChoking,
		"interested", peer.amInterested,
		"active", peer.pipeline.active,
		"pieces", peer.pipeline.pieces,
		"pieces_len", len(peer.pipeline.pieces),
		// "inflight", peer.pipeline.pending,
		// "len_inflight", len(peer.pipeline.pending),
	)
}
