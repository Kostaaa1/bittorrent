package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent"
	"github.com/Kostaaa1/bittorrent/internal/torrent/pipeline"
	"github.com/Kostaaa1/bittorrent/internal/torrent/scheduler"
	"github.com/Kostaaa1/bittorrent/internal/torrent/shared"
)

type Peer struct {
	conn           net.Conn
	addr           string
	bitfield       shared.Bitfield
	amChoking      bool
	amInterested   bool
	peerChoking    bool
	peerInterested bool
	info           *torrent.TorrentInfo
	log            *logger.Log
	pipeline       *pipeline.Pipeline

	writerCh chan<- PieceMessage
	eventCh  chan<- scheduler.Event
	assignCh chan []int
}

func New(
	conn net.Conn,
	info *torrent.TorrentInfo,
	writerCh chan<- PieceMessage,
	eventCh chan<- scheduler.Event,
	log *logger.Log,
) *Peer {
	return &Peer{
		addr:           conn.RemoteAddr().String(),
		info:           info,
		conn:           conn,
		writerCh:       writerCh,
		eventCh:        eventCh,
		assignCh:       make(chan []int, 1),
		log:            log,
		peerChoking:    true,
		amInterested:   false,
		amChoking:      false,
		peerInterested: false,
		// bitfield: ,
		// pipeline: ,
	}
}

// TODO: function for ticking and eliminating pending requests, keep track  amount of times it reassigned the piece with this method. If it happened many times, unassign all pieces from it (but keep unchoked and interested)
// func (peer *Peer) CanAssign() bool {
// 	if !peer.canRequest() {
// 		return false
// 	}
// 	return peer.pipeline.CanAssign()
// }

// func (peer *Peer) UnassignPiece(piece int) {
// 	peer.pipeline.unassign(piece)
// 	peer.log.Assignment("[UNASSIGN SINGLE PIECE]",
// 		"piece", piece,
// 		"peer", peer.Addr,
// 		"assigned", peer.pipeline.pieces,
// 	)
// }

// func (peer *Peer) Unassign(pieces []int) {
// 	for _, piece := range pieces {
// 		peer.pipeline.unassign(piece)
// 	}
// 	peer.log.Assignment("[UNASSIGN PIECES]",
// 		"pieces", pieces,
// 		"peer", peer.Addr,
// 		"assigned", peer.pipeline.pieces,
// 	)
// }

// func (peer *Peer) Assign(pieces []int) {
// 	for _, piece := range pieces {
// 		if ok := peer.pipeline.assign(piece); !ok {
// 			panic("IT NEEDS TO ASSIGN THE PIECE")
// 		}
// 	}
// 	peer.log.Assignment("[ASSIGNED]",
// 		"new_pieces", pieces,
// 		"peer", peer.Addr,
// 		"assigned", peer.pipeline.pieces,
// 	)
// }

// func (peer *Peer) Assigned() []int {
// 	if peer.pipeline == nil {
// 		return nil
// 	}
// 	return peer.pipeline.Assigned()
// }

// func (peer *Peer) Capacity() int {
// 	return peer.pipeline.Capacity()
// }

// func (peer *Peer) CanAssignPiece(piece int) bool {
// 	if !peer.canRequest() {
// 		return false
// 	}
// 	if !peer.hasPiece(piece) {
// 		return false
// 	}
// 	return true
// }

// state returns slog key/value pairs describing this peer (and its pipeline,
// once created), for logging.
func (peer *Peer) state() []any {
	args := []any{
		"peer", peer.addr,
		"peer_choking", peer.peerChoking,
		"am_interested", peer.amInterested,
		"am_choking", peer.amChoking,
		"peer_interested", peer.peerInterested,
		"can_request", peer.canRequest(),
		"bitfield_nil", peer.bitfield == nil,
		"bitfield_bytes", len(peer.bitfield),
	}
	if peer.pipeline == nil {
		return append(args, "pipeline", "nil")
	}
	return append(args, peer.pipeline.State()...)
}

func (peer *Peer) dispatch() {
	if !peer.canRequest() {
		peer.log.Assignment("[DISPATCH] skipped: cannot request", peer.state()...)
		return
	}

	peer.log.Assignment("[DISPATCH] calling pipeline.Dispatch", peer.state()...)

	if err := peer.pipeline.Dispatch(); err != nil {
		if errors.Is(err, pipeline.ErrFailedToAssignNext) {
			peer.log.Assignment("[DISPATCH] pipeline out of work -> sending WANT_PIECES (may block)",
				append(peer.state(), "err", err)...)

			peer.eventCh <- scheduler.EventWantPieces(
				peer.addr,
				peer.pipeline.Capacity())

			peer.log.Assignment("[DISPATCH] WANT_PIECES sent", peer.state()...)
		} else {
			peer.log.Assignment("[DISPATCH] pipeline error (unhandled)",
				append(peer.state(), "err", err)...)
		}
		return
	}

	peer.log.Assignment("[DISPATCH] pipeline.Dispatch ok", peer.state()...)
}

func (peer *Peer) assignPieces(pieces []int) []int {
	peer.log.Assignment("[ASSIGN] enter",
		append(peer.state(), "incoming", pieces, "incoming_len", len(pieces))...)

	if !peer.canRequest() {
		peer.log.Assignment("[ASSIGN] aborted: cannot request, dropping pieces",
			append(peer.state(), "dropped", pieces)...)
		return nil
	}

	toassign := make([]int, 0)
	for _, piece := range pieces {
		if !peer.canRequest() {
			peer.log.Assignment("[ASSIGN] aborted mid-filter: cannot request",
				append(peer.state(), "kept_so_far", toassign, "dropped", pieces)...)
			return nil
		}
		if !peer.hasPiece(piece) {
			peer.log.Assignment("[ASSIGN] filtered out: peer lacks piece",
				append(peer.state(), "piece", piece)...)
			continue
		}
		toassign = append(toassign, piece)
	}

	peer.log.Assignment("[ASSIGN] filtered",
		append(peer.state(),
			"incoming", pieces,
			"incoming_len", len(pieces),
			"kept", toassign,
			"kept_len", len(toassign),
		)...)

	peer.pipeline.AssignPieces(toassign)
	peer.dispatch()

	peer.log.Assignment("[ASSIGN] done", append(peer.state(), "assigned", toassign)...)

	return toassign
}

func (peer *Peer) hasPiece(piece int) bool {
	if peer.bitfield == nil {
		return true
	}
	return peer.bitfield.HasPiece(piece)
}

func (peer *Peer) canRequest() bool {
	return peer.amInterested && !peer.peerChoking
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
		return fmt.Errorf("failed handshake: info hashes do not match: %s", peer.addr)
	}

	return nil
}

func (peer *Peer) Close() error {
	return peer.conn.Close()
}

func (peer *Peer) Listen(ctx context.Context, hs Handshake, b shared.Bitfield) error {
	defer peer.conn.Close()

	if err := peer.initiateHandshake(hs); err != nil {
		peer.log.Error("[HANDSHAKE]", "status", "failed", "error", err)
		peer.conn.Close()
		return err
	}

	peer.log.Traffic("[HANDSHAKE]", "status", "success", "peer", peer.addr)

	peer.sendBitfield(b)
	peer.sendInterested()

	peer.log.Traffic("[SEND] bitfield + interested",
		append(peer.state(), "our_bitfield_bytes", len(b))...)

	windowSize := 10
	maxAssigned := 10

	peer.pipeline = pipeline.New(
		peer.addr,
		windowSize,
		maxAssigned,
		peer.info,
		peer.log,
		func(piece, begin, block int) {
			peer.sendRequest(piece, begin, block)
		},
	)

	peer.log.Debug("[LISTEN] loop starting", peer.state()...)
	defer func() { peer.log.Debug("[LISTEN] loop exited", peer.state()...) }()

	for {
		select {
		case <-ctx.Done():
			peer.log.Debug("[LISTEN] ctx done", append(peer.state(), "err", ctx.Err())...)
			return nil

		case pieces, open := <-peer.assignCh:
			peer.log.Assignment(
				"[LISTEN] <- assignCh",
				append(peer.state(),
					"pieces", pieces,
					"pieces_len", len(pieces),
					"channel_open", open,
				)...)
			peer.assignPieces(pieces)

		default:
			msg, err := readMessage(peer.conn)
			if err != nil {
				if errors.Is(err, io.EOF) {
					peer.log.Debug("[LISTEN] read: EOF, peer closed connection", peer.state()...)
					return nil
				}
				peer.log.Error("[LISTEN] read: failed",
					append(peer.state(), "err", err)...)
				return err
			}

			peer.log.Traffic("[MSG] <- received",
				append(peer.state(),
					"msg", msg.String(),
					"id", uint8(msg.ID),
					"payload_len", len(msg.Payload),
					"payload_nil", msg.Payload == nil,
				)...)

			switch msg.ID {
			case MsgChoke:
				if peer.peerChoking {
					peer.log.Debug("[CHOKE] already choking, ignoring", peer.state()...)
					continue
				}

				peer.peerChoking = true

				assigned := peer.pipeline.Assigned()
				peer.log.Debug("[CHOKE] sending DISCONNECT -> scheduler (may block)",
					append(peer.state(),
						"returning_pieces", assigned,
						"returning_len", len(assigned),
					)...)

				peer.eventCh <- scheduler.EventDisconnect(
					peer.addr,
					assigned,
				)

				peer.log.Debug("[CHOKE] DISCONNECT sent, clearing pipeline", peer.state()...)
				cleared := peer.pipeline.Clear()

				peer.log.Debug("[CHOKE] done",
					append(peer.state(), "cleared", cleared)...)

			case MsgUnchoke:
				peer.peerChoking = false

				peer.log.Debug("[UNCHOKE] sending CONNECT -> scheduler (may block)", peer.state()...)

				capacity := peer.pipeline.Capacity()
				peer.log.Debug("[UNCHOKE] CONNECT sent, sending WANT_PIECES -> scheduler (may block)",
					append(peer.state(), "requesting_capacity", capacity)...)

				peer.eventCh <- scheduler.EventWantPieces(
					peer.addr,
					capacity,
				)

				peer.log.Debug("[UNCHOKE] WANT_PIECES sent", peer.state()...)

			case MsgPiece:
				piece := parsePieceMessage(msg.Payload)

				peer.log.Traffic("[PIECE] parsed, sending -> writerCh (may block)",
					append(peer.state(),
						"piece", piece.Index,
						"begin", piece.Begin,
						"block_len", len(piece.Block),
					)...)

				peer.writerCh <- piece

				peer.log.Traffic("[PIECE] handed to writer",
					append(peer.state(), "piece", piece.Index, "begin", piece.Begin)...)

				peer.pipeline.ReceivedBlock(piece.Index, piece.Begin)
				peer.dispatch()

				peer.log.Traffic(
					"[PIECE] handled",
					append(peer.state(), "piece", piece.Index, "begin", piece.Begin)...)

			case MsgInterested:
				peer.peerInterested = true
				peer.sendUnchoke()
				peer.log.Traffic("[INTERESTED]", "peer", peer.addr)
			case MsgUninterested:
				peer.peerInterested = false
				peer.log.Traffic("[UNINTERESTED]", "peer", peer.addr)
			case MsgBitfield:
				peer.bitfield = msg.Payload
				peer.eventCh <- scheduler.EventConnect(
					peer.addr,
					peer.bitfield,
					peer.assignCh,
				)
				peer.log.Traffic("[BITFIELD] stored",
					append(peer.state(),
						"bytes", len(msg.Payload),
						"advertised_pieces", len(msg.Payload)*8,
						"our_num_pieces", peer.info.NumOfPieces,
					)...)
			case MsgRequest:
				peer.log.Traffic("[REQUEST]", peer.state()...)
			case MsgHave:
				peer.log.Traffic("[HAVE]",
					append(peer.state(), "payload_len", len(msg.Payload))...)
			case MsgCancel:
				peer.log.Traffic("[CANCEL]", peer.state()...)
			case MsgPort:
				peer.log.Traffic("[PORT]", peer.state()...)
			default:
				peer.log.Traffic("[MSG] UNHANDLED id",
					append(peer.state(), "id", uint8(msg.ID))...)
			}
		}
	}
}

func (peer *Peer) Print() {
	peer.log.Info("[PEER]", peer.state()...)
}
