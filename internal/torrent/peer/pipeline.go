package peer

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
		inflight:        0,
		nextBlock:       0,
		pieces:          make(chan int, maxPending),
		curr:            nil,
	}
}

func (p *pipeline) addQueue(piece int) {
	p.pieces <- piece
}

func (p *pipeline) canAssign() bool {
	l := len(p.pieces)
	return l < p.maxPendingLimit
}

func (p *pipeline) destroy() []int {
	left := make([]int, 0)
	if p == nil {
		return left
	}

	for piece := range p.pieces {
		left = append(left, piece)
	}
	p = nil

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
