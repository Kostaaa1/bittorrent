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
	maxAssigned int
	windowSize  int
	nextBlock   int
	pending     map[int]struct{}
	inflight    int
	active      *assignedPiece
	assigned    []int
	mu          sync.Mutex
}

func newPipeline(windowSize, maxAssigned int) *pipeline {
	return &pipeline{
		windowSize:  windowSize,
		maxAssigned: maxAssigned,
		nextBlock:   0,
		pending:     make(map[int]struct{}),
		inflight:    0,

		// assigned:    make(map[int]struct{}),
		// queue:    make(chan int, maxAssigned),

		assigned: make([]int, 0, maxAssigned),
		active:   nil,
	}
}

func (p *pipeline) addPending(piece int) {
	p.mu.Lock()
	p.pending[piece] = struct{}{}
	p.mu.Unlock()
}

func (p *pipeline) canDispatch() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inflight < p.windowSize
}

func (p *pipeline) removePending(piece int) {
	p.mu.Lock()
	if p.active != nil && p.active.pieceID != piece {
		delete(p.pending, piece)
	}
	p.mu.Unlock()
}

func (p *pipeline) CanAssign() bool {
	return len(p.assigned) < p.maxAssigned
}

func (p *pipeline) reassignPieces() []int {
	p.mu.Lock()
	defer p.mu.Unlock()

	l := len(p.assigned)
	if l <= 1 {
		return nil
	}

	c := 0
	reassignLen := l / 2
	reassign := make([]int, reassignLen)

	for _, piece := range p.assigned {
		if c == reassignLen {
			break
		}
		if _, ok := p.pending[piece]; !ok {
			reassign[c] = piece
			c++
		}
	}

	return reassign
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
}

func (p *pipeline) assign(piece int) {
	p.mu.Lock()
	p.assigned = append(p.assigned, piece)
	p.mu.Unlock()
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
	if len(p.assigned) == 0 {
		return -1, false
	}
	for _, piece := range p.assigned {
		if p.active == nil || p.active.pieceID != piece {
			p.nextBlock = 0
			p.active = &assignedPiece{pieceID: piece}
			return piece, true
		}
	}
	return -1, false
}

func (p *pipeline) assignNext() (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.assignNextLocked()
}
