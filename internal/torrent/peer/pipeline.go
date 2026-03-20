package peer

import (
	"errors"
	"math"
	"sync"
	"time"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent"
)

type block struct {
	piece int
	begin int
}

type pipeline struct {
	mu          sync.Mutex
	maxAssigned int
	windowSize  int
	info        *torrent.TorrentInfo
	nextBlock   int
	pieces      []int
	pending     map[block]time.Time
	active      int
	hasActive   bool
	onDispatch  func(piece, begin, block int)
	peerAddr    string
	log         *logger.Log
}

func newPipeline(
	peerAddr string,
	windowSize,
	maxAssigned int,
	info *torrent.TorrentInfo,
	fn func(piece, begin, block int),
	log *logger.Log,
) *pipeline {
	return &pipeline{
		peerAddr:    peerAddr,
		windowSize:  windowSize,
		maxAssigned: maxAssigned,
		nextBlock:   0,
		pending:     make(map[block]time.Time),
		pieces:      make([]int, 0, maxAssigned),
		active:      0,
		hasActive:   false,
		info:        info,
		onDispatch:  fn,
		log:         log,
	}
}

func (p *pipeline) addPendingBlock(piece, begin int) {
	p.mu.Lock()
	p.pending[block{piece, begin}] = time.Now()
	p.mu.Unlock()
}

func (p *pipeline) removePendingBlock(piece, begin int) {
	p.mu.Lock()
	delete(p.pending, block{piece, begin})
	p.mu.Unlock()
}

func (p *pipeline) CanAssign() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pieces) < p.maxAssigned/2
}

func (p *pipeline) Capacity() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxAssigned - len(p.pieces)
}

func (p *pipeline) unassign(pieceID int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.hasActive && p.active == pieceID {
		p.active = 0
		p.hasActive = false
	}

	for id, piece := range p.pieces {
		if pieceID == piece {
			p.pieces = append(p.pieces[:id], p.pieces[id+1:]...)
			break
		}
	}
}

func (p *pipeline) assign(piece int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.pieces) < p.maxAssigned {
		p.pieces = append(p.pieces, piece)
		return true
	}
	return false
}

func (p *pipeline) removePendingBlocks(piece int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// NOTE: is it safe to delete while looping, or i need to copy?
	for req := range p.pending {
		if req.piece == piece {
			delete(p.pending, req)
		}
	}
}

func (p *pipeline) reassign(piece int) {
	p.removePendingBlocks(piece)
}

func (p *pipeline) Assigned() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pieces
}

func (p *pipeline) reset() []int {
	p.mu.Lock()
	defer p.mu.Unlock()

	cap := len(p.pieces)
	if p.hasActive {
		cap++
	}

	toReassign := make([]int, 0, cap)
	toReassign = append(toReassign, p.pieces...)

	if p.hasActive {
		toReassign = append(toReassign, p.active)
	}

	p.hasActive = false
	p.active = 0
	p.nextBlock = 0
	p.pieces = make([]int, 0, p.maxAssigned)
	p.pending = make(map[block]time.Time)

	return toReassign
}

func (p *pipeline) getActiveOrAssignNext() (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hasActive {
		return p.active, true
	}
	return p.assignNextLocked()
}

func (p *pipeline) assignNextLocked() (int, bool) {
	if len(p.pieces) == 0 {
		return -1, false
	}
	for id, piece := range p.pieces {
		if !p.hasActive || p.active != piece {
			p.nextBlock = 0
			p.hasActive = true
			p.active = piece
			p.pieces = append(p.pieces[:id], p.pieces[id+1:]...)
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

func (p *pipeline) canDispatch() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending) < p.windowSize
}

func (p *pipeline) Dispatch() error {
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
		p.addPendingBlock(index, begin)
		p.onDispatch(index, begin, block)
	}

	return nil
}
