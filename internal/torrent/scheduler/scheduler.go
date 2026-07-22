package scheduler

import (
	"context"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent/shared"
)

type eventType int

const (
	eventPeerConnected eventType = iota
	eventPeerDisconnected
	eventAssigned
	eventWantPieces
	eventUnassign
)

func (t eventType) String() string {
	switch t {
	case eventPeerConnected:
		return "PEER_CONNECTED"
	case eventPeerDisconnected:
		return "PEER_DISCONNECTED"
	case eventAssigned:
		return "ASSIGNED"
	case eventWantPieces:
		return "WANT_PIECES"
	case eventUnassign:
		return "UNASSIGN"
	default:
		return "UNKNOWN"
	}
}

type Event struct {
	addr      string
	eventType eventType
	capacity  int
	bitfield  shared.Bitfield
	assignCh  chan<- []int
	pieces    []int
}

func EventConnect(addr string, bf shared.Bitfield, assignCh chan<- []int) Event {
	return Event{
		addr:      addr,
		bitfield:  bf,
		assignCh:  assignCh,
		eventType: eventPeerConnected,
	}
}

func EventDisconnect(addr string, pieces []int) Event {
	return Event{
		addr:      addr,
		pieces:    pieces,
		eventType: eventPeerDisconnected,
	}
}

func EventUnassignPieces(addr string, pieces []int) Event {
	return Event{
		addr:      addr,
		pieces:    pieces,
		eventType: eventUnassign,
	}
}

func EventWantPieces(addr string, capacity int) Event {
	return Event{
		addr:      addr,
		capacity:  capacity,
		eventType: eventWantPieces,
	}
}

func EventAssigned(addr string, pieces []int) Event {
	return Event{
		addr:      addr,
		pieces:    pieces,
		eventType: eventAssigned,
	}
}

type peerState struct {
	bitfield shared.Bitfield
	assignCh chan<- []int
}

type Scheduler struct {
	assigned   map[int]string
	unassigned map[int]struct{}
	peers      map[string]*peerState
	events     chan Event
	log        *logger.Log
}

func New(pieces [][20]byte, log *logger.Log) *Scheduler {
	unassigned := make(map[int]struct{}, len(pieces))
	for index := range pieces {
		unassigned[index] = struct{}{}
	}
	log.Sched("[SCHED] created", "total_pieces", len(pieces), "unassigned", len(unassigned))
	return &Scheduler{
		events:     make(chan Event),
		assigned:   make(map[int]string),
		peers:      make(map[string]*peerState, 0),
		unassigned: unassigned,
		log:        log,
	}
}

func (s *Scheduler) Channel() chan<- Event {
	return s.events
}

func (s *Scheduler) Close(ctx context.Context) {}

// pools returns slog key/value pairs describing the piece pools, for logging.
func (s *Scheduler) pools() []any {
	return []any{
		"unassigned", len(s.unassigned),
		"assigned", len(s.assigned),
		"peers", len(s.peers),
	}
}

func (s *Scheduler) addPeer(e Event) {
	_, ok := s.peers[e.addr]
	if ok {
		s.log.Sched("[SCHED] add peer: already registered, ignoring",
			append(s.pools(), "peer", e.addr)...)
		return
	}
	s.peers[e.addr] = &peerState{
		bitfield: e.bitfield,
		assignCh: e.assignCh,
	}
	s.log.Sched("[SCHED] add peer: registered",
		append(s.pools(),
			"peer", e.addr,
			"bitfield_nil", e.bitfield == nil,
			"bitfield_bytes", len(e.bitfield),
			"assign_ch_nil", e.assignCh == nil,
		)...)
}

func (s *Scheduler) removePeer(e Event) {
	peer, ok := s.peers[e.addr]
	if !ok {
		s.log.Sched("[SCHED] remove peer: NOT FOUND (about to panic)",
			append(s.pools(), "peer", e.addr)...)
		panic("assert: missing peer when attept to delete it")
	}
	close(peer.assignCh)
	delete(s.peers, e.addr)
	s.log.Sched("[SCHED] remove peer: deregistered, assignCh closed",
		append(s.pools(), "peer", e.addr)...)
}

func (s *Scheduler) markPiecesAssigned(e Event) {
	_, ok := s.peers[e.addr]
	if !ok {
		s.log.Sched("[SCHED] mark assigned: peer NOT FOUND (about to panic)",
			append(s.pools(), "peer", e.addr, "pieces", e.pieces)...)
		panic("assert: missing peer when marking pieces assigned")
	}
	before := len(s.assigned)
	for _, piece := range e.pieces {
		delete(s.assigned, piece)
		s.unassigned[piece] = struct{}{}
	}
	s.log.Sched("[SCHED] mark assigned",
		append(s.pools(),
			"peer", e.addr,
			"pieces", e.pieces,
			"count", len(e.pieces),
			"assigned_before", before,
			"assigned_after", len(s.assigned),
		)...)
}

func (s *Scheduler) markPiecesUnassigned(e Event) {
	beforeUnassigned := len(s.unassigned)
	beforeAssigned := len(s.assigned)
	for _, piece := range e.pieces {
		delete(s.assigned, piece)
		s.unassigned[piece] = struct{}{}
	}
	s.log.Sched("[SCHED] mark unassigned: pieces returned to pool",
		append(s.pools(),
			"peer", e.addr,
			"pieces", e.pieces,
			"count", len(e.pieces),
			"unassigned_before", beforeUnassigned,
			"unassigned_after", len(s.unassigned),
			"assigned_before", beforeAssigned,
			"assigned_after", len(s.assigned),
		)...)
}

func (s *Scheduler) assignPiecesToPeer(ctx context.Context, e Event) {
	peer, ok := s.peers[e.addr]
	if !ok {
		s.log.Sched("[SCHED] want pieces: peer NOT REGISTERED (about to panic)",
			append(s.pools(), "peer", e.addr, "capacity", e.capacity)...)
		panic("assert: missing peer when assigning pieces")
	}

	s.log.Sched("[SCHED] want pieces: request received",
		append(s.pools(),
			"peer", e.addr,
			"capacity", e.capacity,
			"bitfield_nil", peer.bitfield == nil,
			"bitfield_bytes", len(peer.bitfield),
		)...)

	pieces := make([]int, 0, e.capacity)
	scanned, lacked := 0, 0

	for piece := range s.unassigned {
		scanned++
		if peer.bitfield.HasPiece(piece) {
			pieces = append(pieces, piece)
		} else {
			lacked++
		}
		if len(pieces) == e.capacity {
			break
		}
	}

	if len(pieces) == 0 {
		s.log.Sched("[SCHED] want pieces: NOTHING TO ASSIGN",
			append(s.pools(),
				"peer", e.addr,
				"capacity", e.capacity,
				"scanned", scanned,
				"peer_lacks", lacked,
			)...)
		return
	}

	// default: channel full; don't block

	s.log.Sched("[SCHED] want pieces: sending -> peer.assignCh (may block)",
		append(s.pools(),
			"peer", e.addr,
			"pieces", pieces,
			"count", len(pieces),
			"capacity", e.capacity,
			"scanned", scanned,
			"peer_lacks", lacked,
		)...)

	select {
	case <-ctx.Done():
		s.log.Sched("[SCHED] want pieces: ABORTED, ctx done before delivery",
			append(s.pools(), "peer", e.addr, "pieces", pieces)...)
	case peer.assignCh <- pieces:
		s.log.Sched("[SCHED] want pieces: delivered",
			append(s.pools(), "peer", e.addr, "pieces", pieces, "count", len(pieces))...)
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	s.log.Sched("[SCHED] run: started", s.pools()...)
	defer func() { s.log.Sched("[SCHED] run: stopped", s.pools()...) }()

	for {
		select {
		case <-ctx.Done():
			s.log.Sched("[SCHED] run: ctx done", append(s.pools(), "err", ctx.Err())...)
			return
		case e := <-s.events:
			s.log.Sched("[SCHED] <- event",
				append(s.pools(),
					"type", e.eventType,
					"peer", e.addr,
					"capacity", e.capacity,
					"pieces", e.pieces,
				)...)

			switch e.eventType {
			case eventPeerConnected:
				s.addPeer(e)
			case eventPeerDisconnected:
				s.removePeer(e)
				s.markPiecesUnassigned(e)
			case eventUnassign:
				s.markPiecesUnassigned(e)
			case eventAssigned:
				s.markPiecesAssigned(e)
			case eventWantPieces:
				s.assignPiecesToPeer(ctx, e)
			default:
				s.log.Sched("[SCHED] run: UNHANDLED event type",
					append(s.pools(), "type", e.eventType, "peer", e.addr)...)
			}
		}
	}
}
