package client

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"test/internal/torrent"
	"test/internal/torrent/peer"
	"test/internal/torrent/tracker"
	"time"
)

type Client struct {
	ID       [20]byte
	Port     uint16
	Bitfield peer.Bitfield

	assigned   map[int]uint64
	unassigned map[int]struct{}
	peers      map[uint64]*peer.Peer

	mu        sync.Mutex
	writer    *peer.PieceWriter
	announcer *tracker.Announcer
	info      *torrent.TorrentInfo
	// keep track of active peers
	peerCh      <-chan tracker.PeerAddress
	peerCounter uint64
	// keep track of connections
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
		peers:       make(map[uint64]*peer.Peer, 0),
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
	c.peers[peer.ID] = peer
	c.mu.Unlock()
}

func (c *Client) removePeer(peer *peer.Peer) {
	pieces := peer.AssignedPieces()
	c.mu.Lock()
	delete(c.peers, peer.ID)
	for _, piece := range pieces {
		c.unassigned[piece] = struct{}{}
	}
	c.mu.Unlock()
}

func (c *Client) freePiece(pieceID int) {
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
func (c *Client) fillPeerPipeline(peer *peer.Peer) {
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

func (c *Client) assignedPeer(pieceID int) *peer.Peer {
	c.mu.Lock()
	assigned := c.assigned[pieceID]
	peer := c.peers[assigned]
	c.mu.Unlock()
	if peer == nil {
		panic("PEER IS NIL")
	}
	return peer
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
				"piece_index", result.Index,
				"erorr", result.Err,
			)
			c.freePiece(result.Index)
		} else {
			c.log.Debug(
				"[DOWNLOAD]",
				"piece_index", result.Index,
				"piece_offset", result.Begin,
				"piece_length", result.LenBlock,
			)
			// increment the number of downloaded
			c.announcer.IncDownloaded(uint64(result.LenBlock))
			// update bitfield
			c.Bitfield.SetPiece(result.Index)
			// assign
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
			peer.OnUnchoke = func() {
				c.fillPeerPipeline(peer)
			}
			peer.OnChoke = func(pieces []int) {
				c.log.Info("OnChoke RAN", "free_pieces", pieces)
				for _, piece := range pieces {
					c.freePiece(piece)
				}
			}
			peer.OnHandshake = func() {
				c.addPeer(peer)
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
		c.mu.Unlock()
		fmt.Println("ACTIVE PEER CONNECTIONS", len(peers))
		for _, peer := range peers {
			peer.Print()
		}
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
