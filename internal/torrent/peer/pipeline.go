package peer

import (
	"sync"
)

type assignedPiece struct {
	pieceID int
	// pendingBlocks []int
}

// TODO:
// if slow peer has dispatched requests, and peer does not send the pieces for those requests, there is no way of getting dispatched pieces back (they need to be reassigned). change data structure for pieces to []int.
type pipeline struct {
	// boundaries
	maxAssigned int
	windowSize  int

	inflight  int
	nextBlock int
	// current active piece that is being requested
	active *assignedPiece

	queue    chan int
	assigned []int
	// if peer respond with piece that fails the hash verification, meaning that peer does not have valid piece, then we blacklist the piece so we cannot assign it to this peer again
	// blacklist map[int]struct{}
	mu sync.Mutex
}

func newPipeline(windowSize, maxAssigned int) *pipeline {
	return &pipeline{
		windowSize:  windowSize,
		maxAssigned: maxAssigned,
		inflight:    0,
		nextBlock:   0,
		queue:       make(chan int, maxAssigned),
		// assigned:    make(map[int]struct{}),
		assigned: make([]int, 0, maxAssigned),
		active:   nil,
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
	defer p.mu.Unlock()
	if p.active != nil && p.active.pieceID == pieceID {
		p.active = nil
	}
	for id, piece := range p.assigned {
		if pieceID == piece {
			p.assigned = append(p.assigned[:id], p.assigned[id+1:]...)
			break
		}
	}
	// delete(p.assigned, pieceID)
}

func (p *pipeline) assign(pieceID int) {
	if len(p.queue) < p.maxAssigned {
		p.queue <- pieceID
		// p.assigned[piece] = struct{}{}
		p.assigned = append(p.assigned, pieceID)
	}
}

func (p *pipeline) assignedPieces() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	// pieces := make([]int, 0, len(p.assigned))
	// for piece := range p.assigned {
	// 	pieces = append(pieces, piece)
	// }
	return p.assigned
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

		// p.active = piece
		p.nextBlock = 0
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
