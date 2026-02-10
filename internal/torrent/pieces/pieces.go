package pieces

import (
	"sync"
	"test/internal/torrent/peer"
)

type PieceManager struct {
	bitfield   peer.Bitfield
	mu         sync.Mutex
	assigned   map[int]uint64
	unassigned map[int]uint64
	peers      []*peer.Peer
}

func (pm *PieceManager) SetPiece(pieceId int) {
	pm.bitfield.SetPiece(pieceId)
}

func (pm *PieceManager) AddPeer(peer *peer.Peer) {
	pm.mu.Lock()
	pm.peers = append(pm.peers, peer)
	pm.mu.Unlock()
}

func (pm *PieceManager) RemovePeer(peer *peer.Peer) {}

func NewPieceManager(pieces [][20]byte) *PieceManager {
	// TODO: do this via channel in separate goroutine...
	unassigned := make(map[int]uint64, len(pieces))
	for index := range pieces {
		unassigned[index] = 0
	}
	return &PieceManager{
		bitfield:   make([]byte, (len(pieces)+7)/8),
		assigned:   make(map[int]uint64),
		unassigned: unassigned,
		peers:      make([]*peer.Peer, 0),
	}
}

func (pm *PieceManager) GetAssignedPeer(pieceIndex int) *peer.Peer {
	pm.mu.Lock()
	peerID, found := pm.assigned[pieceIndex]
	pm.mu.Unlock()
	if !found {
		return nil
	}
	return pm.peers[peerID]
}

func (pm *PieceManager) FreePiece(pieceIndex int) {
	pm.mu.Lock()
	delete(pm.assigned, pieceIndex)
	pm.unassigned[pieceIndex] = 0
	pm.mu.Unlock()
}

func (pm *PieceManager) FillPeerQueue(peer *peer.Peer) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for peer.CanAssign() {
		assigned := false
		for pieceID := range pm.unassigned {
			if peer.HasPiece(pieceID) {
				delete(pm.unassigned, pieceID)
				pm.assigned[pieceID] = peer.ID

				peer.AddPieceToQueue(pieceID)

				assigned = true
				break
			}
		}
		if !assigned {
			break
		}
	}
}
