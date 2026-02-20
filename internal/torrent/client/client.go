package client

import (
	"fmt"
	"log/slog"
	"sync"
	"test/internal/torrent/io"
	"test/internal/torrent/peer"
	"test/internal/torrent/tracker"
	"time"
)

type Client struct {
	Bitfield peer.Bitfield
	// TODO: switch from map to slices
	assigned   map[int]uint64
	unassigned map[int]uint64
	peers      map[uint64]*peer.Peer
	mu         sync.Mutex
	log        *slog.Logger
}

func (c *Client) AddPeer(peer *peer.Peer) {
	c.mu.Lock()
	c.peers[peer.ID] = peer
	c.mu.Unlock()
}

func (c *Client) RemovePeer(peer *peer.Peer) {
	pieces := peer.UnassignPieces()
	c.mu.Lock()
	delete(c.peers, peer.ID)
	for _, piece := range pieces {
		c.unassigned[piece] = 0
	}
	c.mu.Unlock()
}

func (c *Client) Debugger() {
	tick := time.NewTicker(time.Second * 15)
	for range tick.C {
		c.mu.Lock()
		peers := c.peers
		c.mu.Unlock()
		fmt.Println("ACTIVE PEERS:", peers)
		for _, p := range peers {
			p.Print()
		}
	}
}

func New(log *slog.Logger, pieces [][20]byte) *Client {
	// TODO:
	unassigned := make(map[int]uint64, len(pieces))
	for index := range pieces {
		unassigned[index] = 0
	}
	return &Client{
		Bitfield:   make([]byte, (len(pieces)+7)/8),
		assigned:   make(map[int]uint64),
		unassigned: unassigned,
		peers:      make(map[uint64]*peer.Peer, 0),
		log:        log,
	}
}

func (c *Client) FreePiece(pieceIndex int) {
	c.mu.Lock()
	delete(c.assigned, pieceIndex)
	c.unassigned[pieceIndex] = 0
	c.mu.Unlock()
}

func (c *Client) assignPiece(peer *peer.Peer, pieceID int) {
	delete(c.unassigned, pieceID)
	c.assigned[pieceID] = peer.ID
	peer.AddPieceToQueue(pieceID)
}

// TODO: If unnassigned is empty, try to assign the piece from other peer queue
func (c *Client) FillPeerQueue(peer *peer.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// assign piece to peer as long as it can accept

	for peer.CanAssign() {
		assigned := false
		for pieceID := range c.unassigned {
			if peer.HasPiece(pieceID) {
				c.assignPiece(peer, pieceID)
				assigned = true
				break
			}
		}
		if !assigned {
			break
		}
	}
}

// func (c *Client) NotifyPeers(pieceIndex int) {
// 	c.mu.Lock()
// 	peers := c.peers
// 	c.mu.Unlock()
// 	fmt.Println("Sending have: ", pieceIndex)
// 	for _, peer := range peers {
// 		if err := peer.SendHave(pieceIndex); err != nil {
// 			fmt.Printf("failed to send HAVE message to peer: %s - closing and removing\n", peer.Addr)
// 			c.RemovePeer(peer)
// 		}
// 	}
// }

func (c *Client) GetAssignedPeer(pieceIndex int) *peer.Peer {
	c.mu.Lock()
	peerID, found := c.assigned[pieceIndex]
	c.mu.Unlock()
	if !found {
		return nil
	}
	return c.peers[peerID]
}

func (c *Client) setPiece(pieceID int) {
	c.Bitfield.SetPiece(pieceID)
	peer := c.GetAssignedPeer(pieceID)
	c.FillPeerQueue(peer)
}

func (c *Client) CollectResults(ann *tracker.Announcer, logger *slog.Logger, results <-chan io.Result) {
	for result := range results {
		if result.Err != nil {
			c.log.Debug(
				"[FAILED TO DOWNLOAD]",
				"piece_index", result.Index,
				"erorr", result.Err,
			)
			c.FreePiece(result.Index)
		} else {
			c.log.Debug(
				"[DOWNLOAD]",
				"piece_index", result.Index,
				"piece_offset", result.Begin,
				"piece_length", result.LenBlock,
			)
			ann.IncDownloaded(uint64(result.LenBlock))
			c.setPiece(result.Index)
			// notify all peers that we have a piece
			// c.NotifyPeers(result.Index)
		}
	}
}
