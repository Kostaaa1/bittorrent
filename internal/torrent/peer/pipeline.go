package peer

import "sync"

// TODO: improve with channel
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

func (pp *pipeline) addQueue(piece int) {
	pp.mu.Lock()
	pp.pieces[piece] = true
	pp.mu.Unlock()
}

func (pp *pipeline) canAssign() bool {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	l := len(pp.pieces)
	return l < pp.maxPendingLimit
}

func (pp *pipeline) assignNext() bool {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	if len(pp.pieces) == 0 {
		pp.curr = nil
		return false
	}

	for piece := range pp.pieces {
		delete(pp.pieces, piece)
		p := piece
		pp.curr = &p
		return true
	}

	return false
}
