package peer

import "sync"

// pipeline is responsible to dispatch the requests for current (assigned piece).
// each peer can have up to maxPendingLimit assigned pieces
type pipeline struct {
	// window size represents the limit of inflight requests
	windowSize int
	// inflight requests bounded by the window size
	inflight int
	// tracking next block to request
	nextBlock int
	// queue for assigned pieces
	// pieces          chan int
	pieces          map[int]bool
	mu              sync.Mutex
	maxPendingLimit int
	// active assigned piece
	curr *int
}

func newPipeline() *pipeline {
	windowSize := 16
	maxPending := 10
	return &pipeline{
		windowSize:      windowSize,
		maxPendingLimit: maxPending,
		inflight:        0,
		nextBlock:       0,
		// pieces:          make(chan int, maxPending),
		pieces: make(map[int]bool),
		curr:   nil,
	}
}

func (p *pipeline) addQueue(piece int) {
	p.mu.Lock()
	p.pieces[piece] = true
	p.mu.Unlock()
}

func (p *pipeline) canAssign() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	l := len(p.pieces)
	return l < p.maxPendingLimit
}

// func (p *pipeline) drain() []int {
// 	if p == nil {
// 		return nil
// 	}
// 	p.mu.Lock()
// 	left := make([]int, len(p.pieces))
// 	for piece := range p.pieces {
// 		left = append(left, piece)
// 	}
// 	p.mu.Unlock()
// 	p = nil
// 	return left
// }

func (p *pipeline) assignNext() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.pieces) == 0 {
		p.curr = nil
		return false
	}

	for piece := range p.pieces {
		delete(p.pieces, piece)
		c := piece
		p.curr = &c
		return true
	}

	return false
}
