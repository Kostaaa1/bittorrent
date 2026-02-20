package peer

type assignment struct {
	// used to pop the pieces from the queue and assign them to curr
	queue chan int
	// when piece is received from peer, we delete from assigned
	// if some period of time peer does not send the piece at all, we use this map to reassign the pieces to different peer.
	snapshot map[int]struct{}
	// current active piece that is being requested
	active *int
}

// TODO:
// if slow peer has dispatched requests, and peer does not send the pieces for those requests, there is no way of getting dispatched pieces back (they need to be reassigned). change data structure for pieces to []int.
type pipeline struct {
	windowSize  int
	inflight    int
	nextBlock   int
	maxAssigned int
	assignment  assignment
}

func newPipeline() *pipeline {
	windowSize := 10
	maxAssigned := 10
	return &pipeline{
		windowSize:  windowSize,
		maxAssigned: maxAssigned,
		inflight:    0,
		nextBlock:   0,
		assignment: assignment{
			queue:    make(chan int, maxAssigned),
			snapshot: make(map[int]struct{}),
			active:   nil,
		},
	}
}

func (p *pipeline) canAssign() bool {
	return len(p.assignment.queue) < p.maxAssigned
}

func (p *pipeline) addPiece(piece int) {
	if len(p.assignment.queue) < p.maxAssigned {
		p.assignment.queue <- piece
		p.assignment.snapshot[piece] = struct{}{}
	}
}

// used when piece arrives, checks if new piece arrived (different from current), meaning the active/current is downloaded
// already popped from queue, removing from snapshot
func (p *pipeline) donePiece(pieceIndex int) {
	current := *p.assignment.active
	if current != pieceIndex {
		delete(p.assignment.snapshot, pieceIndex)
	}
}

func (p *pipeline) assignedPieces() []int {
	pieces := make([]int, 0)
	for piece := range p.assignment.snapshot {
		pieces = append(pieces, piece)
	}
	return pieces
}

// pop from queue
func (p *pipeline) assignNext() (int, bool) {
	piece, ok := <-p.assignment.queue
	if !ok {
		return -1, false
	}
	p.nextBlock = 0
	p.assignment.active = &piece
	return piece, true
}

func (p *pipeline) getActiveOrAssignNext() (int, bool) {
	if p.assignment.active != nil {
		return *p.assignment.active, true
	}
	return p.assignNext()
}
