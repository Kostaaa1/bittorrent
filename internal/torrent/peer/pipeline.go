package peer

import (
	"sync"
)

// TODO:
// if slow peer has dispatched requests, and peer does not send the pieces for those requests, there is no way of getting dispatched pieces back (they need to be reassigned). change data structure for pieces to []int.
type pipeline struct {
	windowSize  int
	inflight    int
	nextBlock   int
	maxAssigned int
	// used to pop the pieces from the queue and assign them to curr
	queue chan int
	// when piece is received from peer, we delete from assigned
	// if some period of time peer does not send the piece at all, we use this map to reassign the pieces to different peer.
	assigned  map[int]struct{}
	blacklist map[int]struct{}
	// current active piece that is being requested
	active *int
	mu     sync.Mutex
}

func newPipeline() *pipeline {
	windowSize := 10
	maxAssigned := 2
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

func (p *pipeline) CanAssign() bool {
	return len(p.queue) < p.maxAssigned
}

func (p *pipeline) CanAssignPiece(pieceID int) bool {
	p.mu.Lock()
	_, ok := p.blacklist[pieceID]
	p.mu.Unlock()
	return !ok
}

func (p *pipeline) unassignPiece(pieceID int) {
	p.mu.Lock()
	delete(p.assigned, pieceID)
	p.mu.Unlock()
}

func (p *pipeline) addPiece(piece int) {
	if len(p.queue) < p.maxAssigned {
		p.queue <- piece
		p.assigned[piece] = struct{}{}
	}
}

func (p *pipeline) assignedPieces() []int {
	pieces := make([]int, 0, len(p.assigned))
	for piece := range p.assigned {
		pieces = append(pieces, piece)
	}
	return pieces
}

func (p *pipeline) getActiveOrAssignNext() (int, bool) {
	if p.active != nil {
		active := *p.active
		return active, true
	}
	return p.assignNext()
}

func (p *pipeline) assignNext() (int, bool) {
	select {
	case piece, ok := <-p.queue:
		if !ok {
			p.active = nil
			return -1, false
		}
		p.nextBlock = 0
		p.active = &piece
		return piece, true
	default:
		p.active = nil
		return -1, false
	}
}
