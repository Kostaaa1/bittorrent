package client

import (
	"context"

	"github.com/Kostaaa1/bittorrent/internal/torrent/shared"
)

type Type int

const (
	PeerConnected Type = iota
	PeerDisonnected
	AssignPieces
	UnassignPieces
	UnassignPiece
)

type Event struct {
	Addr     string
	Type     Type
	Capacity int
	Pieces   []int
	Bitfield shared.Bitfield
}

type peerState struct {
	bitfield shared.Bitfield
}

type Scheduler struct {
	assigned   map[int]string
	unassigned map[int]struct{}
	peers      map[string]peerState
	events     chan Event
}

func newScheduler(pieces [][20]byte) *Scheduler {
	unassigned := make(map[int]struct{}, len(pieces))
	for index := range pieces {
		unassigned[index] = struct{}{}
	}

	return &Scheduler{
		events:     make(chan Event),
		assigned:   make(map[int]string),
		unassigned: unassigned,
		peers:      make(map[string]peerState, 0),
	}
}

func (s *Scheduler) Close(ctx context.Context) {
}

func (s *Scheduler) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-s.events:
			switch ev.Type {
			case PeerConnected:
				s.peers[ev.Addr] = peerState{bitfield: ev.Bitfield}
			case PeerDisonnected:
				s.removePeer(ev.Addr)
			case UnassignPiece:
				s.unassign(ev.Pieces[0])
			case UnassignPieces:
				s.unassignPeerPieces(ev.Addr)
			case AssignPieces:
				s.assign(ev)
			}
		}
	}
}

func (s *Scheduler) removePeer(addr string) {
	s.unassignPeerPieces(addr)
	delete(s.peers, addr)
}

func (s *Scheduler) unassignPeerPieces(addr string) {
	copied := s.assigned
	for piece, peerAddr := range copied {
		if peerAddr == addr {
			s.unassign(piece)
		}
	}
}

// make piece available again, so it can be assigned to the peer
func (s *Scheduler) unassign(piece int) {
	delete(s.assigned, piece)
	s.unassigned[piece] = struct{}{}
}

func (s *Scheduler) assign(evMsg Event) {
	// if target == nil {
	// 	panic("target peer cannot be nil")
	// }

	// if !target.CanAssign() {
	// 	c.log.Assignment(
	// 		"target is not assignable",
	// 		"peer", target.Addr,
	// 		"pieces", target.Assigned(),
	// 	)
	// 	return
	// }

	// cap := target.Capacity()
	cap := evMsg.Capacity
	pieces := make([]int, 0, cap)
	n := 0

	unassigned := s.unassigned
	// c.log.Assignment("target is assignable",
	// 	"target", target.Addr,
	// 	"pieces", target.Assigned(),
	// 	"missing", cap,
	// 	"unassigned", len(c.unassigned),
	// )

	peer := s.peers[evMsg.Addr]

	if len(unassigned) > 0 {
		for piece := range unassigned {
			// if target.CanAssignPiece(piece) {
			if peer.bitfield.HasPiece(piece) {
				pieces = append(pieces, piece)
				n++
			}
			if cap == n {
				break
			}
		}
	} else {
		// TODO: could possibly take portion of pieces from other peers (choose them randomly or by their effectiveness)
		// c.log.Debug("NO MORE IN UNASSIGNED",
		// 	"target", target.Addr,
		// 	"missing", cap,
		// 	"pieces", target.Assigned(),
		// )
	}

	if len(pieces) > 0 {
		// TODO: assign these pieces to peer pipeline
		// target.Assign(pieces)
		for _, piece := range pieces {
			// c.assign(target, piece)
			delete(s.unassigned, piece)
			s.assigned[piece] = evMsg.Addr
		}
	}
}
