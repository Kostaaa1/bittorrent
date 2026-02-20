package peer

// TODO:
// if slow peer has dispatched requests, and peer does not send the pieces for those requests, there is no way of getting dispatched pieces back (they need to be reassigned). change data structure for pieces to []int.
type pipeline struct {
	windowSize      int
	inflight        int
	nextBlock       int
	pieces          chan int
	maxPendingLimit int
	curr            *int
}

func newPipeline() *pipeline {
	windowSize := 10
	maxPending := 10
	return &pipeline{
		windowSize:      windowSize,
		maxPendingLimit: maxPending,
		pieces:          make(chan int, maxPending),
		inflight:        0,
		nextBlock:       0,
		curr:            nil,
	}
}

func (p *pipeline) addQueue(piece int) {
	p.pieces <- piece
}

func (p *pipeline) canAssign() bool {
	return len(p.pieces) < p.maxPendingLimit
}

func (p *pipeline) drain() []int {
	left := make([]int, 0)
	if p == nil {
		return left
	}
	for piece := range p.pieces {
		left = append(left, piece)
	}
	return left
}

func (p *pipeline) assignNext() bool {
	p.nextBlock = 0

	if len(p.pieces) == 0 {
		p.curr = nil
		return false
	}

	for piece := range p.pieces {
		c := piece
		p.curr = &c
		return true
	}

	return false
}
