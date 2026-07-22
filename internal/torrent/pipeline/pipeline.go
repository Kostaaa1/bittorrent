package pipeline

import (
	"errors"
	"math"
	"sort"
	"time"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent"
)

var (
	ErrFailedToAssignNext = errors.New("failed to assign new piece")
)

type requestPiece struct {
	pending     []int
	requestedAt time.Time
}

type Pipeline struct {
	maxAssigned  int
	windowSize   int
	nextBlock    int
	pendingCount int
	active       int
	info         *torrent.TorrentInfo
	pieces       map[int]*requestPiece
	hasActive    bool
	peerAddr     string
	onDispatch   func(piece, begin, block int)
	log          *logger.Log
}

func New(
	peerAddr string,
	windowSize,
	maxAssigned int,
	info *torrent.TorrentInfo,
	log *logger.Log,
	fn func(piece, begin, block int),
) *Pipeline {
	log.Pipe("[PIPE] created",
		"peer", peerAddr,
		"window_size", windowSize,
		"max_assigned", maxAssigned,
		"num_pieces", info.NumOfPieces,
		"blocks_per_piece", info.NumBlocksPerPiece,
		"block_size", info.BlockSize,
	)
	return &Pipeline{
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

// pieceIDs returns the currently held piece indices, sorted, for logging.
func (p *Pipeline) pieceIDs() []int {
	ids := make([]int, 0, len(p.pieces))
	for piece := range p.pieces {
		ids = append(ids, piece)
	}
	sort.Ints(ids)
	return ids
}

// pendingBlocks returns the total number of in-flight block offsets per piece,
// for logging. pendingCount is the counter the window check actually uses.
func (p *Pipeline) pendingBlocks() int {
	n := 0
	for _, req := range p.pieces {
		n += len(req.pending)
	}
	return n
}

// State returns slog key/value pairs describing this pipeline, for logging.
func (p *Pipeline) State() []any {
	if p == nil {
		return []any{"pipeline", "nil"}
	}
	return []any{
		"peer", p.peerAddr,
		"active", p.active,
		"has_active", p.hasActive,
		"next_block", p.nextBlock,
		"pending_count", p.pendingCount,
		"pending_actual", p.pendingBlocks(),
		"window_size", p.windowSize,
		"held_pieces", p.pieceIDs(),
		"held_len", len(p.pieces),
		"capacity", p.Capacity(),
	}
}

// func (p *Pipeline) unassign(piece int) {
// 	if p.hasActive && p.active == piece {
// 		p.active = 0
// 		p.hasActive = false
// 	}
// 	delete(p.pieces, piece)
// }

// func (p *Pipeline) assign(piece int) bool {
// 	if len(p.pieces) < p.maxAssigned {
// 		p.pieces[piece] = &requestPiece{pending: make([]int, 0)}
// 		return true
// 	}
// 	return false
// }

func (p *Pipeline) Clear() []int {
	p.log.Pipe("[PIPE] clear: enter", p.State()...)

	toReassign := p.Assigned()
	if p.hasActive {
		toReassign = append(toReassign, p.active)
	}
	p.hasActive = false
	p.active = 0
	p.nextBlock = 0
	p.pieces = make(map[int]*requestPiece)

	p.log.Pipe("[PIPE] clear: done",
		append(p.State(),
			"returned", toReassign,
			"returned_len", len(toReassign),
		)...)

	return toReassign
}

func (p *Pipeline) getActiveOrAssignNext() (int, bool) {
	if p.hasActive {
		return p.active, true
	}
	return p.next()
}

func (p *Pipeline) next() (int, bool) {
	if len(p.pieces) == 0 {
		p.log.Pipe("[PIPE] next: no pieces held -> cannot activate", p.State()...)
		return -1, false
	}
	for piece := range p.pieces {
		if !p.hasActive || p.active != piece {
			prev, hadPrev := p.active, p.hasActive
			p.nextBlock = 0
			p.hasActive = true
			p.active = piece
			p.log.Pipe("[PIPE] next: activated piece",
				append(p.State(), "prev_active", prev, "had_prev_active", hadPrev)...)
			return piece, true
		}
	}
	p.log.Pipe("[PIPE] next: no candidate other than active -> failed", p.State()...)
	return -1, false
}

func (p *Pipeline) canDispatch() bool {
	if !p.hasActive {
		return false
	}
	return p.pendingCount < p.windowSize
}

func (p *Pipeline) addPendingBlock(piece, begin int) {
	request, ok := p.pieces[piece]
	if !ok {
		p.log.Pipe("[PIPE] add pending block: PIECE NOT HELD (about to panic)",
			append(p.State(), "piece", piece, "begin", begin)...)
		panic("piece missing?")
	} else {
		request.pending = append(request.pending, begin)
		request.requestedAt = time.Now()
		p.pendingCount++
	}
}

func (p *Pipeline) ReceivedBlock(piece, begin int) {
	req := p.pieces[piece]

	p.log.Pipe("[PIPE] received block",
		append(p.State(),
			"piece", piece,
			"begin", begin,
			"piece_held", req != nil,
		)...)

	for id, block := range req.pending {
		if block == begin {
			p.pendingCount--
			req.pending = append(req.pending[:id], req.pending[id+1:]...)
			p.log.Pipe("[PIPE] received block: matched pending request",
				append(p.State(), "piece", piece, "begin", begin)...)
			return
		}
	}

	p.log.Pipe("[PIPE] received block: NO MATCHING pending request",
		append(p.State(),
			"piece", piece,
			"begin", begin,
			"piece_pending", req.pending,
		)...)
}

func (p *Pipeline) AssignPieces(pieces []int) {
	p.log.Pipe("[PIPE] assign pieces: enter",
		append(p.State(), "incoming", pieces, "incoming_len", len(pieces))...)

	if len(pieces) != p.Capacity() {
		p.log.Pipe("[PIPE] assign pieces: LEN != CAPACITY (about to panic)",
			append(p.State(),
				"incoming", pieces,
				"incoming_len", len(pieces),
				"capacity", p.Capacity(),
			)...)
		panic("assert: capacity and pieces are not same length")
	}

	for _, piece := range pieces {
		if _, exists := p.pieces[piece]; exists {
			p.log.Pipe("[PIPE] assign pieces: piece ALREADY HELD, overwriting",
				append(p.State(), "piece", piece)...)
		}
		p.pieces[piece] = &requestPiece{pending: make([]int, 0)}
	}

	p.log.Pipe("[PIPE] assign pieces: done",
		append(p.State(), "incoming", pieces)...)
}

// func (p *Pipeline) Clear() { p.clear() }
// func (p *Pipeline) CanAssign() bool {
// 	return len(p.pieces) < p.maxAssigned/2
// }

func (p *Pipeline) Capacity() int {
	return p.maxAssigned - len(p.pieces)
}

func (p *Pipeline) Assigned() []int {
	pieces := make([]int, len(p.pieces))
	for piece := range p.pieces {
		pieces = append(pieces, piece)
	}
	return pieces
}

func (p *Pipeline) Dispatch() error {
	p.log.Pipe("[PIPE] dispatch: enter",
		append(p.State(), "can_dispatch", p.canDispatch())...)

	if !p.hasActive {
		index, ok := p.next()
		if !ok {
			p.log.Pipe("[PIPE] dispatch: EXIT ErrFailedToAssignNext (nothing to activate)",
				p.State()...)
			return ErrFailedToAssignNext
		}
		_ = index
	}

	dispatched := 0

	for p.canDispatch() {
		index, ok := p.getActiveOrAssignNext()
		if !ok {
			p.log.Pipe("[PIPE] dispatch: EXIT ErrFailedToAssignNext (getActiveOrAssignNext)",
				append(p.State(), "dispatched", dispatched)...)
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
			p.log.Pipe("[PIPE] dispatch: piece exhausted, advancing",
				append(p.State(),
					"piece", index,
					"blocks_for_piece", blocksForPiece,
					"dispatched", dispatched,
				)...)
			index, ok = p.next()
			if !ok {
				p.log.Pipe("[PIPE] dispatch: EXIT ErrFailedToAssignNext (piece exhausted, no next)",
					append(p.State(), "dispatched", dispatched)...)
				return ErrFailedToAssignNext
			}
		}

		begin := p.nextBlock * block

		if index == lastPieceID {
			size := p.info.TotalLength - (lastPieceID * p.info.PieceLength)
			remaining := size - begin
			if remaining < 0 {
				p.log.Pipe("[PIPE] dispatch: REMAINING < 0 (about to panic)",
					append(p.State(),
						"piece", index,
						"begin", begin,
						"size", size,
						"remaining", remaining,
					)...)
				panic("TODO: REMAINING IS LESS THEN 0")
			}
			if remaining < block {
				block = remaining
			}
		}

		p.nextBlock++
		p.addPendingBlock(index, begin)

		p.log.Pipe("[PIPE] dispatch: -> request",
			"peer", p.peerAddr,
			"piece", index,
			"begin", begin,
			"block", block,
			"next_block", p.nextBlock,
			"blocks_for_piece", blocksForPiece,
			"pending_count", p.pendingCount,
			"window_size", p.windowSize,
		)

		p.onDispatch(index, begin, block)
		dispatched++
	}

	p.log.Pipe("[PIPE] dispatch: exit ok",
		append(p.State(),
			"dispatched", dispatched,
			"can_dispatch", p.canDispatch(),
		)...)

	return nil
}
