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
	Bitfield     peer.Bitfield
	assigned     map[int]uint64
	unassigned   map[int]struct{}
	peers        map[uint64]*peer.Peer
	mu           sync.Mutex
	log          *slog.Logger
	maxPeerLimit int
}

func (c *Client) AddPeer(peer *peer.Peer) {
	c.mu.Lock()
	c.peers[peer.ID] = peer
	c.mu.Unlock()
}

func (c *Client) RemovePeer(peer *peer.Peer) {
	pieces := peer.AssignedPieces()
	c.mu.Lock()
	delete(c.peers, peer.ID)
	for _, piece := range pieces {
		c.unassigned[piece] = struct{}{}
	}
	c.mu.Unlock()
}

func (c *Client) PrintPeers() {
	tick := time.NewTicker(time.Second * 30)
	for range tick.C {
		c.mu.Lock()
		peers := c.peers
		c.mu.Unlock()
		fmt.Println("ACTIVE PEERS:", len(peers))
		for _, p := range peers {
			p.Print()
		}
	}
}

func New(pieces [][20]byte, log *slog.Logger) *Client {
	unassigned := make(map[int]struct{}, len(pieces))
	for index := range pieces {
		unassigned[index] = struct{}{}
	}
	return &Client{
		Bitfield:   make([]byte, (len(pieces)+7)/8),
		assigned:   make(map[int]uint64),
		unassigned: unassigned,
		peers:      make(map[uint64]*peer.Peer, 0),
		log:        log,
	}
}

func (c *Client) FreePiece(pieceID int) {
	c.mu.Lock()
	delete(c.assigned, pieceID)
	c.unassigned[pieceID] = struct{}{}
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

func (c *Client) CollectResults(ann *tracker.Announcer, results <-chan io.Result) {
	for result := range results {
		if result.Err != nil {
			c.log.Debug(
				"[FAILED TO DOWNLOAD]",
				"piece_index", result.Index,
				"erorr", result.Err,
			)
			c.FreePiece(result.Index)
		} else {
			// increment the number of downloaded
			// update bitfield
			// notify/send have message to all peers that we have a piece
			// remove it from assigned

			c.log.Debug(
				"[DOWNLOAD]",
				"piece_index", result.Index,
				"piece_offset", result.Begin,
				"piece_length", result.LenBlock,
			)
			ann.IncDownloaded(uint64(result.LenBlock))

			pieceID := result.Index
			c.Bitfield.SetPiece(pieceID)

			c.mu.Lock()
			peer := c.peers[c.assigned[pieceID]]
			c.mu.Unlock()
			if peer == nil {
				panic("peer is nil")
			}

			peer.UnassignPiece(pieceID)
			c.FillPeerQueue(peer)

			// notify all peers that we have a piece
			// c.NotifyPeers(result.Index)
		}
	}
}

// type Info struct {
// 	InfoHash          [20]byte
// 	NumOfPieces       int
// 	TotalLength       int
// 	PieceLength       int
// 	BlockSize         int
// 	NumBlocksPerPiece int
// }

// func (c *Client) NotifyPeers(pieceID int) {
// 	c.mu.Lock()
// 	peers := c.peers
// 	c.mu.Unlock()
// 	fmt.Println("Sending have: ", pieceID)
// 	for _, peer := range peers {
// 		if err := peer.SendHave(pieceID); err != nil {
// 			fmt.Printf("failed to send HAVE message to peer: %s - closing and removing\n", peer.Addr)
// 			c.RemovePeer(peer)
// 		}
// 	}
// }
