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
	inflight    map[int]struct{}
	active      *assignedPiece
	queue       chan int
	assigned    []int
	mu          sync.Mutex
}

func newPipeline(windowSize, maxAssigned int) *pipeline {
	return &pipeline{
		windowSize:  windowSize,
		maxAssigned: maxAssigned,
		nextBlock:   0,
		// assigned:    make(map[int]struct{}),
		inflight: make(map[int]struct{}),
		queue:    make(chan int, maxAssigned),
		assigned: make([]int, 0, maxAssigned),
		active:   nil,
	}
}

func (p *pipeline) addInflight(piece int) {
	p.mu.Lock()
	p.inflight[piece] = struct{}{}
	p.mu.Unlock()
}

func (p *pipeline) removeInflight(piece int) {
	p.mu.Lock()
	if p.active.pieceID != piece {
		delete(p.inflight, piece)
	}
	p.mu.Unlock()
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

	c := 0
	reassignLen := l / 2
	reassign := make([]int, reassignLen)

	for i, piece := range p.assigned {
		if c == reassignLen {
			break
		}
		if _, ok := p.inflight[piece]; !ok {
			reassign[i] = piece
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
		// p.nextBlock = 0
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
