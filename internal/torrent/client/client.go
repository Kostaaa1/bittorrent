package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"test/internal/torrent"
	"test/internal/torrent/peer"
	"test/internal/torrent/tracker"
	"time"
)

type Client struct {
	ID                   [20]byte
	Port                 uint16
	Bitfield             peer.Bitfield
	assigned             map[int]uint64
	unassigned           map[int]struct{}
	peers                []*peer.Peer
	mu                   sync.Mutex
	writer               *peer.PieceWriter
	announcer            *tracker.Announcer
	info                 *torrent.TorrentInfo
	peerCh               <-chan tracker.PeerAddress
	peerCounter          uint64
	maxGlobalConnections int
	maxConnectedPeers    int
	maxHalfOpen          int
	log                  *slog.Logger
}

func New(
	clientID [20]byte,
	port uint16,
	info *torrent.TorrentInfo,
	pieces [][20]byte,
	files []*torrent.FileEntry,
	announce string,
	announceList [][]string,
	log *slog.Logger,
) *Client {
	unassigned := make(map[int]struct{}, len(pieces))
	for index := range pieces {
		unassigned[index] = struct{}{}
	}

	peerCh := make(chan tracker.PeerAddress)

	writer := peer.NewPieceWriter(info, pieces, files)
	announcer := tracker.NewAnnouncer(
		info.InfoHash,
		clientID,
		port,
		uint64(info.TotalLength),
		peerCh,
		announce,
		announceList,
		true,
	)

	return &Client{
		ID:          clientID,
		Port:        port,
		Bitfield:    make([]byte, (len(pieces)+7)/8),
		assigned:    make(map[int]uint64),
		peers:       make([]*peer.Peer, 0),
		unassigned:  unassigned,
		writer:      writer,
		announcer:   announcer,
		peerCh:      peerCh,
		peerCounter: 0,
		info:        info,
		log:         log,
		// maxConnectedPeers: 1,
	}
}

func (c *Client) addPeer(peer *peer.Peer) {
	c.mu.Lock()
	c.peers = append(c.peers, peer)
	c.mu.Unlock()
}

func (c *Client) removePeer(target *peer.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pieces := target.AssignedPieces(); len(pieces) > 0 {
		c.peers = slices.DeleteFunc(c.peers, func(p *peer.Peer) bool {
			return p.ID == target.ID
		})
		for _, piece := range pieces {
			c.unassigned[piece] = struct{}{}
		}
	}
}

func (c *Client) unassign(pieceID int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.assigned, pieceID)
	c.unassigned[pieceID] = struct{}{}
}

func (c *Client) assign(peer *peer.Peer, pieceID int) bool {
	if !peer.CanAssign() {
		return false
	}
	delete(c.unassigned, pieceID)
	c.assigned[pieceID] = peer.ID
	peer.AddPieceToQueue(pieceID)
	return true
}

func (c *Client) assignPieces(peer *peer.Peer, pieces []int) {
	c.mu.Lock()
	defer c.mu.Lock()
	for _, piece := range pieces {
		c.assign(peer, piece)
	}
}

var ErrNoPeers = errors.New("failed to assign pieces: 0 peers")
var ErrFailedAssignment = errors.New("failed to assign pieces: no peers can accept the pieces")

// rearrange
func (c *Client) rearrangePieces(skip *peer.Peer, pieces []int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.peers) == 0 {
		return ErrNoPeers
	}

	lastPeerID := 0
	totalPeers := len(c.peers)

	for _, piece := range pieces {
		found := false

		for i := 0; i < totalPeers; i++ {
			idx := (lastPeerID + i) % totalPeers
			peer := c.peers[idx]

			if skip != nil && skip.ID == peer.ID {
				continue
			}

			lastPeerID = (idx + 1) % totalPeers
			c.assign(peer, piece)
			found = true
			break
		}

		if !found {
			return ErrFailedAssignment
		}
	}

	return nil
}

// Fills peers request pipeline as long as peer can accept the new piece to request.
// If there are unassigned pieces, that map will be used for assagning
// If not, we take portion of assigned pieces from other peer and reassign
func (c *Client) fillPeerPipeline(target *peer.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.unassigned) > 0 {
		for target.CanAssign() {
			assigned := false
			for pieceID := range c.unassigned {
				if target.HasPiece(pieceID) {
					c.assign(target, pieceID)
					assigned = true
					break
				}
			}
			if !assigned {
				break
			}
		}
	} else {
		c.log.Info("[UNASSIGNING FROM OTHER PEERS]")
		for _, peer := range c.peers {
			if peer.ID != target.ID {
				if !target.CanAssign() {
					break
				}

				// get portion of assigned peers
				newpieces := peer.ReassignPieces()
				// track assigned pieces so we can unassign them from original peer
				unassign := make([]int, 0)

				for target.CanAssign() {
					assigned := false

					for piece := range newpieces {
						if target.HasPiece(piece) {
							c.assign(target, piece)
							// we need to unnassign this piece from peer
							unassign = append(unassign, piece)
							assigned = true
							break
						}
					}

					if !assigned {
						break
					}
				}

				if len(unassign) > 0 {
					for _, piece := range unassign {
						peer.UnassignPiece(piece)
					}
				}
			}
		}
	}
}

func (c *Client) assignedPeer(pieceID int) *peer.Peer {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, peer := range c.peers {
		if peer.ID == c.assigned[pieceID] {
			return peer
		}
	}

	panic("PEER IS NIL")
	return nil
}

func (c *Client) collectResults(results <-chan peer.Result) {
	// Accept the results from writer
	// if piece failed (hash verification failed or write failed or whatever)
	// - make it availble again for assignment (for client/scheduler)
	// - and remove it from peer
	for result := range results {
		// find peer and remove it from assigned slice
		// assign new pieces as long as peer can accept it
		peer := c.assignedPeer(result.Index)
		peer.UnassignPiece(result.Index)

		if result.Err != nil {
			c.log.Debug(
				"[FAILED TO DOWNLOAD]",
				"piece", result.Index,
				"erorr", result.Err,
			)
			c.unassign(result.Index)
		} else {
			c.log.Debug(
				"[DOWNLOAD]",
				"piece", result.Index,
				"piece_offset", result.Begin,
				"piece_length", result.LenBlock,
			)
			// increment the number of downloaded
			c.announcer.IncDownloaded(uint64(result.LenBlock))
			// update bitfield
			c.Bitfield.SetPiece(result.Index)
			// assign new pieces to peer
			c.fillPeerPipeline(peer)
			// notify/send have message to all peers that we have a piece
			// c.NotifyPeers(result.Index)
		}
	}
}

func (c *Client) Run(ctx context.Context) {
	hs := peer.Handshake{
		Pstr:      []byte("BitTorrent protocol"),
		Reserverd: [8]byte{},
		PeerID:    c.ID,
		InfoHash:  c.info.InfoHash,
	}

	writerC, resultC := c.writer.Channels()

	go c.announcer.Run(ctx)
	go c.writer.Run()
	go c.collectResults(resultC)
	go c.printPeers()

	var peerSem chan struct{}
	if c.maxConnectedPeers > 0 {
		peerSem = make(chan struct{}, c.maxConnectedPeers)
	}

	for p := range c.peerCh {
		go func() {
			if c.maxConnectedPeers > 0 {
				peerSem <- struct{}{}
				defer func() { <-peerSem }()
			}

			conn, err := net.DialTimeout("tcp", p.IP4Addr(), time.Second*5)
			if err != nil {
				c.log.Error("failed to dial", "error", err)
				return
			}

			peerID := atomic.AddUint64(&c.peerCounter, 1) - 1

			peer := peer.New(peerID, conn, c.info, writerC, c.log)
			peer.OnHandshake = func() {
				c.addPeer(peer)
			}
			peer.OnUnchoke = func() {
				c.fillPeerPipeline(peer)
			}
			peer.OnUnassign = func(pieces []int) {
				c.log.Info("OnUnassign", "free_pieces", pieces)
				if err := c.rearrangePieces(peer, pieces); err != nil {
					panic(err)
				}
			}
			peer.OnMissingPiece = func() {
				fmt.Println("OnMissing PIECE")
				// Peer has no pieces left
				c.fillPeerPipeline(peer)
			}

			if err := peer.Open(ctx, hs, c.Bitfield); err != nil {
				c.log.Error("[PEER DISCONNECT]", "error: failed to read message", err)
				c.removePeer(peer)
				return
			}
		}()
	}
}

func (c *Client) printPeers() {
	ticker := time.NewTicker(time.Second * 30)
	for range ticker.C {
		c.mu.Lock()
		peers := c.peers
		fmt.Println("ACTIVE PEER CONNECTIONS", len(peers))
		for _, peer := range peers {
			peer.Print()
		}
		c.mu.Unlock()
	}
}

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
