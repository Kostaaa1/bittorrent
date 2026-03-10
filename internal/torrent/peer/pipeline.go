package peer

import (
	"errors"
	"math"
	"sync"
	"test/internal/torrent"
	"time"
)

type assignedPiece struct {
	pieceID int
	// pendingBlocks []int
}

type pending struct {
	piece int
	begin int
}

// TODO:
// if slow peer has dispatched requests, and peer does not send the pieces for those requests, there is no way of getting dispatched pieces back (they need to be reassigned). change data structure for pieces to []int.
type pipeline struct {
	mu          sync.Mutex
	maxAssigned int
	windowSize  int
	info        *torrent.TorrentInfo
	nextBlock   int
	assigned    []int
	pending     map[pending]time.Time
	active      *assignedPiece
	onDispatch  func(piece, begin, block int)
}

func newPipeline(
	windowSize,
	maxAssigned int,
	info *torrent.TorrentInfo,
	fn func(piece, begin, block int),
) *pipeline {
	return &pipeline{
		windowSize:  windowSize,
		maxAssigned: maxAssigned,
		nextBlock:   0,
		pending:     make(map[pending]time.Time),
		assigned:    make([]int, 0, maxAssigned),
		active:      nil,
		info:        info,
		onDispatch:  fn,
	}
}

func (p *pipeline) addPending(piece, begin int) {
	p.mu.Lock()
	p.pending[pending{piece, begin}] = time.Now()
	p.mu.Unlock()
}

func (p *pipeline) removePending(piece, begin int) {
	p.mu.Lock()
	delete(p.pending, pending{piece, begin})
	p.mu.Unlock()
}

func (p *pipeline) canDispatch() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending) < p.windowSize
}

func (p *pipeline) CanAssign() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.assigned) < p.maxAssigned
}

func (p *pipeline) unassign(pieceID int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != nil && p.active.pieceID == pieceID {
		panic("yoo")
		// p.active = nil
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

func (p *pipeline) missing() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.windowSize - len(p.assigned)
}

func (p *pipeline) drain() []int {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.active = nil
	p.pending = nil
	p.nextBlock = 0
	p.onDispatch = nil
	assigned := p.assigned
	p.assigned = nil

	return assigned
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
	for id, piece := range p.assigned {
		if p.active == nil || p.active.pieceID != piece {
			p.nextBlock = 0
			p.active = &assignedPiece{pieceID: piece}
			p.assigned = append(p.assigned[:id], p.assigned[id+1:]...)
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

var ErrFailedToAssignNext = errors.New("failed to assign new piece")

func (p *pipeline) dispatch() error {
	for p.canDispatch() {
		index, ok := p.getActiveOrAssignNext()
		if !ok {
			return ErrFailedToAssignNext
		}

		block := p.info.BlockSize
		numOfPieces := p.info.NumOfPieces
		lastPieceID := numOfPieces - 1
		blocksForPiece := p.info.NumBlocksPerPiece

		if index == lastPieceID {
			size := p.info.TotalLength - (lastPieceID * p.info.PieceLength)
			blocksForPiece = int(math.Ceil(float64(size) / float64(block)))
		}

		if p.nextBlock >= blocksForPiece {
			index, ok = p.assignNext()
			if !ok {
				return ErrFailedToAssignNext
			}
		}

		begin := p.nextBlock * block

		if index == lastPieceID {
			size := p.info.TotalLength - (lastPieceID * p.info.PieceLength)
			remaining := size - begin
			if remaining < 0 {
				panic("TODO: REMAINING IS LESS THEN 0")
			}
			if remaining < block {
				block = remaining
			}
		}

		p.nextBlock++
		p.addPending(index, begin)
		p.onDispatch(index, begin, block)
	}

	return nil
}
