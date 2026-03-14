package client

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent"
	"github.com/Kostaaa1/bittorrent/internal/torrent/peer"
	"github.com/Kostaaa1/bittorrent/internal/torrent/tracker"
)

var ErrNoPeers = errors.New("failed to assign pieces: 0 peers")
var ErrFailedAssignment = errors.New("failed to assign pieces: no peers can accept the pieces")

type Client struct {
	ID                   [20]byte
	Port                 uint16
	Bitfield             peer.Bitfield
	assigned             map[int]uint64
	unassigned           map[int]struct{}
	writer               *peer.PieceWriter
	announcer            *tracker.Announcer
	info                 *torrent.TorrentInfo
	maxGlobalConnections int
	maxConnectedPeers    int
	maxHalfOpen          int
	log                  *logger.Log
	// peer
	peers       []*peer.Peer
	peerCh      chan tracker.PeerAddress
	peerCounter uint64
	mu          sync.Mutex
}

func New(
	clientID [20]byte,
	port uint16,
	info *torrent.TorrentInfo,
	pieces [][20]byte,
	files []*torrent.FileEntry,
	announce string,
	announceList [][]string,
	log *logger.Log,
) *Client {
	unassigned := make(map[int]struct{}, len(pieces))
	for index := range pieces {
		unassigned[index] = struct{}{}
	}

	peerCh := make(chan tracker.PeerAddress)
	writer := peer.NewPieceWriter(info, pieces, files)

	// go func() {
	// 	peerCh <- tracker.PeerAddress{
	// 		IP:   "91.90.126.105",
	// 		Port: 35680,
	// 	}
	// 	peerCh <- tracker.PeerAddress{
	// 		IP:   "2.216.38.247",
	// 		Port: 62103,
	// 	}
	// 	peerCh <- tracker.PeerAddress{
	// 		IP:   "73.165.143.252",
	// 		Port: 31503,
	// 	}
	// 	peerCh <- tracker.PeerAddress{
	// 		IP:   "188.6.176.56:46423",
	// 		Port: 31503,
	// 	}
	// }()

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
		unassigned:  unassigned,
		peers:       make([]*peer.Peer, 0),
		writer:      writer,
		announcer:   announcer,
		peerCh:      peerCh,
		peerCounter: 0,
		info:        info,
		log:         log,
		// maxConnectedPeers: 3,
	}
}

func (c *Client) addPeer(peer *peer.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.peers = append(c.peers, peer)
}

// removes peer from slice of peers and reassign pieces
func (c *Client) removePeer(target *peer.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pieces := target.Assigned(); len(pieces) > 0 {
		c.peers = slices.DeleteFunc(c.peers, func(p *peer.Peer) bool {
			return p.ID == target.ID
		})
		for _, piece := range pieces {
			c.unassigned[piece] = struct{}{}
		}
	}
}

func (c *Client) unassign(piece int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.assigned, piece)
	c.unassigned[piece] = struct{}{}
}

// func (c *Client) assign(peer *peer.Peer, pieces []int) {
// 	peer.Assign(pieces)
// 	for _, piece := range pieces {
// 		delete(c.unassigned, piece)
// 		c.assigned[piece] = peer.ID
// 	}
// }

func (c *Client) fillPeerPipeline(target *peer.Peer) {
	if target == nil {
		panic("target peer cannot be nil")
	}

	if !target.Assignable() {
		c.log.Assignment(
			"target is not assignable",
			"peer", target.Addr,
			"pieces", target.Assigned(),
		)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	missing := target.Missing()
	pieces := make([]int, 0, missing)
	n := 0

	c.log.Assignment("target is assignable",
		"target", target.Addr,
		"pieces", target.Assigned(),
		"missing", missing,
		"unassigned", len(c.unassigned),
	)

	if len(c.unassigned) > 0 {
		for piece := range c.unassigned {
			if target.CanAssignPiece(piece) {
				pieces = append(pieces, piece)
				n++
			}
			if missing == n {
				break
			}
		}

		if len(pieces) > 0 {
			// assign pieces to peer
			target.Assign(pieces)
			// iterate through pieces and delete from unassigned and add to assigned
			for _, piece := range pieces {
				delete(c.unassigned, piece)
				c.assigned[piece] = target.ID
			}
		}
	} else {
		c.log.Assignment("NO MORE IN UNASSIGNED",
			"target", target.Addr,
			"missing", missing,
			"pieces", target.Assigned(),
		)

		// for _, peer := range c.peers {
		// 	if peer.ID == target.ID {
		// 		continue
		// 	}

		// 	peerPieces := peer.Reassign()
		// 	if peerPieces == nil || len(peerPieces) == 0 {
		// 		continue
		// 	}

		// 	c.log.Assignment("portion of pieces",
		// 		"peer_pieces", peerPieces,
		// 		"peer", peer.Addr,
		// 		"count", n,
		// 		"pieces", pieces,
		// 	)
		// 	for _, piece := range peerPieces {
		// 		if target.CanAssignPiece(piece) {
		// 			pieces = append(pieces, piece)
		// 			n++
		// 		}
		// 		if missing == n {
		// 			break
		// 		}
		// 	}
		// 	if len(pieces) > 0 {
		// 		// assign pieces to target
		// 		target.Assign(pieces)
		// 		peer.Unassign(pieces)
		// 		// unassign same pieces from peer
		// 		// for _, piece := range pieces {
		// 		// 	peer.Unassign(piece)
		// 		// }
		// 	}
		// 	if n == len(pieces) {
		// 		break
		// 	}
		// }
		// c.log.Assignment("AFTER ASSIGNING FROM PEERS", "peer", target.Addr, "pieces", target.Assigned())
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
}

func (c *Client) collectResults(results <-chan peer.Result) {
	for result := range results {
		peer := c.assignedPeer(result.Index)
		peer.UnassignPiece(result.Index)

		if result.Err != nil {
			c.log.Write(
				"[FAILED TO DOWNLOAD]",
				"peer", peer.Addr,
				"piece", result.Index,
				"erorr", result.Err,
			)
			c.unassign(result.Index)
		} else {
			c.log.Write(
				"[DOWNLOAD]",
				"peer", peer.Addr,
				"piece", result.Index,
				"piece_offset", result.Begin,
				"piece_length", result.LenBlock,
			)
			// increment the number of downloaded
			// c.announcer.IncDownloaded(uint64(result.LenBlock))
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
				c.log.Debug("on Unchoke, assigning pieces", "peer", peer.Addr)
				c.fillPeerPipeline(peer)
			}
			peer.OnMissingPiece = func() {
				c.log.Info("OnMissing PIECE", "peer", peer.Addr)
				// c.fillPeerPipeline(peer)
			}
			peer.OnUnassign = func(pieces []int) {
				c.log.Info("OnUnassign",
					"peer", peer.Addr,
					"free_pieces", pieces,
				)
				for _, piece := range pieces {
					c.unassign(piece)
				}
				// if err := c.rearrangePieces(peer, pieces); err != nil {
				// 	panic(err)
				// }
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
	tick := time.NewTicker(time.Minute)
	for range tick.C {
		c.mu.Lock()
		peers := c.peers
		c.mu.Unlock()
		for _, peer := range peers {
			peer.Print()
		}
	}
}
