package peer

import (
	"sync"
)

type assignedPiece struct {
	pieceID int
	// blocksToRequest []int
}

// TODO:
// if slow peer has dispatched requests, and peer does not send the pieces for those requests, there is no way of getting dispatched pieces back (they need to be reassigned). change data structure for pieces to []int.
type pipeline struct {
	windowSize  int
	inflight    int
	nextBlock   int
	maxAssigned int
	// current active piece that is being requested
	active *assignedPiece
	// used for assigning active/curr pieces
	queue chan int
	// when piece is received from peer, we delete from assigned
	// if some period of time peer does not send the piece at all, we use this map to reassign the pieces to different peer.
	assigned map[int]struct{}
	// if peer respond with piece that fails the hash verification, then we blacklist the piece so we cannot assign it to this peer again
	blacklist map[int]struct{}
	mu        sync.Mutex
}

func newPipeline() *pipeline {
	windowSize := 10
	maxAssigned := 10
	return &pipeline{
		windowSize:  windowSize,
		maxAssigned: maxAssigned,
		inflight:    0,
		nextBlock:   0,
		queue:       make(chan int, maxAssigned),
		assigned:    make(map[int]struct{}),
		active:      nil,
	}
}

// func (p *pipeline) CanAssignPiece(pieceID int) bool {
// 	p.mu.Lock()
// 	_, ok := p.blacklist[pieceID]
// 	p.mu.Unlock()
// 	return !ok
// }

func (p *pipeline) CanAssign() bool {
	return len(p.queue) < p.maxAssigned
}

func (p *pipeline) unassign(pieceID int) {
	p.mu.Lock()
	delete(p.assigned, pieceID)
	p.mu.Unlock()
}

func (p *pipeline) assign(piece int) {
	if len(p.queue) < p.maxAssigned {
		p.queue <- piece
		p.assigned[piece] = struct{}{}
	}
}

func (p *pipeline) assignedPieces() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	pieces := make([]int, 0, len(p.assigned))
	for piece := range p.assigned {
		pieces = append(pieces, piece)
	}
	return pieces
}

func (p *pipeline) getActiveOrAssignNext() (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != nil {
		return p.active.pieceID, true
	}
	return p.assignNextLocked()
}

func (p *pipeline) assignNextLocked() (int, bool) {
	select {
	case pieceID, ok := <-p.queue:
		if !ok {
			// p.active = 0
			p.active = nil
			return -1, false
		}

		p.nextBlock = 0
		// p.active = piece
		p.active = &assignedPiece{pieceID: pieceID}

		return pieceID, true

	default:
		// p.hasActive = false
		p.active = nil
		return -1, false
	}
}

func (p *pipeline) assignNext() (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.assignNextLocked()
}
