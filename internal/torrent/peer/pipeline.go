package peer

import (
	"errors"
	"math"
	"sync"
	"time"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent"
)

type requestPiece struct {
	pending     []int
	requestedAt time.Time
}

type pipeline struct {
	mu           sync.Mutex
	maxAssigned  int
	windowSize   int
	info         *torrent.TorrentInfo
	nextBlock    int
	pieces       map[int]*requestPiece
	pendingCount int
	active       int
	hasActive    bool
	onDispatch   func(piece, begin, block int)
	peerAddr     string
	log          *logger.Log
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
		pieces:      make(map[int]*requestPiece),
		active:      0,
		hasActive:   false,
		info:        info,
		onDispatch:  fn,
		log:         log,
	}
}

func (p *pipeline) addPendingBlock(piece, begin int) {
	p.mu.Lock()
	request, ok := p.pieces[piece]
	if !ok {
		panic("piece missing?")
	} else {
		request.pending = append(request.pending, begin)
		request.requestedAt = time.Now()
		p.pendingCount++
	}
	p.mu.Unlock()
}

func (p *pipeline) removePendingBlock(piece, begin int) {
	p.mu.Lock()
	req := p.pieces[piece]
	for id, block := range req.pending {
		if block == begin {
			p.pendingCount--
			req.pending = append(req.pending[:id], req.pending[id+1:]...)
			break
		}
	}
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

func (p *pipeline) unassign(piece int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.hasActive && p.active == piece {
		p.active = 0
		p.hasActive = false
	}

	delete(p.pieces, piece)
}

func (p *pipeline) assign(piece int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.pieces) < p.maxAssigned {
		p.pieces[piece] = &requestPiece{pending: make([]int, 0)}
		return true
	}

	return false
}

func (p *pipeline) Assigned() []int {
	p.mu.Lock()
	defer p.mu.Unlock()

	pieces := make([]int, len(p.pieces))
	for piece := range p.pieces {
		pieces = append(pieces, piece)
	}

	return pieces
}

func (p *pipeline) reset() []int {
	p.mu.Lock()
	defer p.mu.Unlock()

	toReassign := p.Assigned()

	if p.hasActive {
		toReassign = append(toReassign, p.active)
	}

	p.hasActive = false
	p.active = 0
	p.nextBlock = 0
	p.pieces = make(map[int]*requestPiece)

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

	for piece := range p.pieces {
		if !p.hasActive || p.active != piece {
			p.nextBlock = 0
			p.hasActive = true
			p.active = piece
			return piece, true
		}
	}

	return -1, false
}

var ErrFailedToAssignNext = errors.New("failed to assign new piece")

func (p *pipeline) canDispatch() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.hasActive {
		return false
	}

	return p.pendingCount < p.windowSize
}

func (p *pipeline) assignNext() (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.assignNextLocked()
}

func (p *pipeline) Dispatch() error {
	if !p.hasActive {
		index, ok := p.assignNext()
		if !ok {
			return ErrFailedToAssignNext
		}
		_ = index
	}

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
