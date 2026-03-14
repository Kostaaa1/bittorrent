package peer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent"
)

type pending struct {
	piece int
	begin int
}

type pipeline struct {
	mu          sync.Mutex
	maxAssigned int
	windowSize  int
	info        *torrent.TorrentInfo
	nextBlock   int
	assigned    []int
	pending     map[pending]time.Time
	// active piece for requesting
	active    int
	hasActive bool
	//
	onDispatch func(piece, begin, block int)
	peerAddr   string
	log        *logger.Log
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
		pending:     make(map[pending]time.Time),
		assigned:    make([]int, 0, maxAssigned),
		active:      0,
		hasActive:   false,
		info:        info,
		onDispatch:  fn,
		log:         log,
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

func (p *pipeline) assignable() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.assigned) < p.maxAssigned/2
}

func (p *pipeline) StartDeadline(
	ctx context.Context,
	tick time.Duration,
	fn func([]int),
) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			pending := p.pending
			p.mu.Unlock()

			toReassign := make([]int, 0)

			for piece, requested := range pending {
				if time.Since(requested) >= time.Second*15 {
					fmt.Println("EXCEEDED REQUEST DEADLINE", piece.piece)
					delete(p.pending, piece)
					toReassign = append(toReassign, piece.piece)
				}
			}

			// p.unassign(piece.piece)
			// fn(toReassign)
		}
	}
}

func (p *pipeline) Missing() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxAssigned - len(p.assigned)
}

func (p *pipeline) unassign(pieceID int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.hasActive && p.active == pieceID {
		p.active = 0
		p.hasActive = false
	}

	for id, piece := range p.assigned {
		if pieceID == piece {
			p.assigned = append(p.assigned[:id], p.assigned[id+1:]...)
			break
		}
	}
}

func (p *pipeline) assign(piece int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.assigned) < p.maxAssigned {
		p.assigned = append(p.assigned, piece)
		return true
	}

	return false
}

func (p *pipeline) Assigned() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.assigned
}

// func (p *pipeline) reassign() []int {
// 	p.mu.Lock()
// 	defer p.mu.Unlock()
// 	if len(p.assigned) == 0 {
// 		return nil
// 	}
// 	pieces := make([]int, len(p.assigned)/2)
// 	copy(pieces, p.assigned)
// 	return pieces
// }

func (p *pipeline) missing() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.windowSize - len(p.assigned)
}

func (p *pipeline) reset() []int {
	p.mu.Lock()
	defer p.mu.Unlock()

	cap := len(p.assigned)
	if p.hasActive {
		cap++
	}

	toReassign := make([]int, 0, cap)
	toReassign = append(toReassign, p.assigned...)

	if p.hasActive {
		toReassign = append(toReassign, p.active)
	}

	p.hasActive = false
	p.active = 0
	p.nextBlock = 0
	p.assigned = make([]int, 0, p.maxAssigned)
	p.pending = make(map[pending]time.Time)

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
	if len(p.assigned) == 0 {
		return -1, false
	}
	for id, piece := range p.assigned {
		if !p.hasActive || p.active != piece {
			p.nextBlock = 0
			p.hasActive = true
			p.active = piece
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

func (p *pipeline) canDispatch() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending) < p.windowSize
}

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
