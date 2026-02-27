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
	inflight    int
	nextBlock   int
	active      *assignedPiece
	queue       chan int
	assigned    []int
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

func (p *pipeline) CanAssign() bool {
	return len(p.queue) < p.maxAssigned
}

func (p *pipeline) reassignPieces() []int {
	p.mu.Lock()
	defer p.mu.Unlock()

	l := len(p.assigned)
	if l <= 1 {
		return nil
	}

	reassignLen := l / 2
	reassign := make([]int, reassignLen)

	for i := 0; i < reassignLen; i++ {
		piece := p.assigned[i]
		reassign[i] = piece
		// p.assigned = append(p.assigned[:i], p.assigned[i+1:]...)
	}

	return reassign
}

// reassign pieces to another peer.

// func (p *pipeline) ReassignPieces() []int {
// 	p.mu.Lock()
// 	defer p.mu.Unlock()

// 	l := len(p.assigned)
// 	if l <= 1 {
// 		return nil
// 	}

// 	reassignLen := l / 2
// 	reassign := make([]int, reassignLen)

// 	for i := 0; i < reassignLen; i++ {
// 		piece := p.assigned[i]
// 		reassign[i] = piece
// 		// p.assigned = append(p.assigned[:i], p.assigned[i+1:]...)
// 	}

// 	return reassign
// }

// func (p *pipeline) unassignPieces(pieces []int) {
// 	for _, piece := range pieces {
// 		p.unassign(piece)
// 	}
// }

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
}

func (p *pipeline) assign(pieceID int) {
	if len(p.queue) < p.maxAssigned {
		p.queue <- pieceID
		p.mu.Lock()
		p.assigned = append(p.assigned, pieceID)
		p.mu.Unlock()
	}
}

func (p *pipeline) assignedPieces() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
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
