package client

import (
	"fmt"
	"log/slog"
	"sync"
	"test/internal/torrent/io"
	"test/internal/torrent/peer"
	"test/internal/torrent/tracker"
)

type Client struct {
	Bitfield peer.Bitfield
	// TODO: switch from map to slices
	assigned   map[int]uint64
	unassigned map[int]uint64
	mu         sync.Mutex
	peers      map[uint64]*peer.Peer
}

func (pm *Client) SetPiece(pieceId int) {
	pm.Bitfield.SetPiece(pieceId)
}

func (pm *Client) AddPeer(peer *peer.Peer) {
	pm.mu.Lock()
	pm.peers[peer.ID] = peer
	pm.mu.Unlock()
}

func (pm *Client) RemovePeer(peer *peer.Peer) error {
	free, _ := peer.Close()

	pm.mu.Lock()
	delete(pm.peers, peer.ID)
	for _, piece := range free {
		pm.unassigned[piece] = 0
	}
	pm.mu.Unlock()

	return nil
}

func New(pieces [][20]byte) *Client {
	unassigned := make(map[int]uint64, len(pieces))
	for index := range pieces {
		unassigned[index] = 0
	}
	return &Client{
		Bitfield:   make([]byte, (len(pieces)+7)/8),
		assigned:   make(map[int]uint64),
		unassigned: unassigned,
		peers:      make(map[uint64]*peer.Peer, 0),
	}
}

func (pm *Client) GetAssignedPeer(pieceIndex int) *peer.Peer {
	pm.mu.Lock()
	peerID, found := pm.assigned[pieceIndex]
	pm.mu.Unlock()
	if !found {
		return nil
	}
	return pm.peers[peerID]
}

func (pm *Client) FreePiece(pieceIndex int) {
	pm.mu.Lock()
	delete(pm.assigned, pieceIndex)
	pm.unassigned[pieceIndex] = 0
	pm.mu.Unlock()
}

// TODO: If unnassigned is empty, try to assign the piece from other peer queue
func (pm *Client) FillPeerQueue(peer *peer.Peer) {
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

func (pm *Client) NotifyPeers(pieceIndex int) {
	pm.mu.Lock()
	peers := pm.peers
	pm.mu.Unlock()
	fmt.Println("Sending have: ", pieceIndex)
	for _, peer := range peers {
		peer.SendHave(pieceIndex)
	}
}

func (pm *Client) CollectResults(ann *tracker.Announcer, logger *slog.Logger, results <-chan io.Result) {
	for result := range results {
		if result.Err != nil {
			pm.FreePiece(result.Index)
		} else {
			ann.IncDownloaded(uint64(result.LenBlock))
			// notify all peers that we have a piece
			pm.NotifyPeers(result.Index)
			pm.SetPiece(result.Index)
			peer := pm.GetAssignedPeer(result.Index)
			pm.FillPeerQueue(peer)
		}
	}
}
