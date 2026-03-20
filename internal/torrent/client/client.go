package client

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"time"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent"
	"github.com/Kostaaa1/bittorrent/internal/torrent/peer"
	"github.com/Kostaaa1/bittorrent/internal/torrent/tracker"
)

var ErrNoPeers = errors.New("failed to assign pieces: 0 peers")
var ErrFailedAssignment = errors.New("failed to assign pieces: no peers can accept the pieces")

type Client struct {
	ID       [20]byte
	Port     uint16
	Bitfield peer.Bitfield

	assigned   map[int]*peer.Peer
	unassigned map[int]struct{}

	writer    *peer.PieceWriter
	announcer *tracker.Announcer
	info      *torrent.TorrentInfo
	log       *logger.Log
	// active peers which passed the handshake
	peers  []*peer.Peer
	peerCh chan tracker.PeerAddress

	mu sync.Mutex

	maxGlobalConnections int
	maxConnectedPeers    int
	maxHalfOpen          int
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

	go func() {
		peerCh <- tracker.PeerAddress{
			IP:   "193.5.16.196",
			Port: 31337,
		}
		peerCh <- tracker.PeerAddress{
			IP:   "2.216.38.247",
			Port: 62103,
		}
	}()

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
		ID:         clientID,
		Port:       port,
		Bitfield:   make([]byte, (len(pieces)+7)/8),
		assigned:   make(map[int]*peer.Peer),
		unassigned: unassigned,
		peers:      make([]*peer.Peer, 0),
		writer:     writer,
		announcer:  announcer,
		peerCh:     peerCh,
		info:       info,
		log:        log,
		// maxConnectedPeers: 3,
	}
}

func (c *Client) addPeer(peer *peer.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.peers = append(c.peers, peer)
}

// removes peer from slice of peers and mark its pieces unassigned
func (c *Client) removePeer(target *peer.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pieces := target.Assigned(); len(pieces) > 0 {
		c.peers = slices.DeleteFunc(c.peers, func(p *peer.Peer) bool {
			// same memory address
			return p == target
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

func (c *Client) schedulePieces(target *peer.Peer) {
	if target == nil {
		panic("target peer cannot be nil")
	}

	if !target.CanAssign() {
		c.log.Assignment(
			"target is not assignable",
			"peer", target.Addr,
			"pieces", target.Assigned(),
		)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cap := target.Capacity()
	pieces := make([]int, 0, cap)
	n := 0

	c.log.Assignment("target is assignable",
		"target", target.Addr,
		"pieces", target.Assigned(),
		"missing", cap,
		"unassigned", len(c.unassigned),
	)

	if len(c.unassigned) > 0 {
		for piece := range c.unassigned {
			if target.CanAssignPiece(piece) {
				pieces = append(pieces, piece)
				n++
			}
			if cap == n {
				break
			}
		}

		if len(pieces) > 0 {
			target.Assign(pieces)
			for _, piece := range pieces {
				delete(c.unassigned, piece)
				c.assigned[piece] = target
			}
		}
	} else {
		c.log.Debug("NO MORE IN UNASSIGNED",
			"target", target.Addr,
			"missing", cap,
			"pieces", target.Assigned(),
		)
	}
}

func (c *Client) collectResults(results <-chan peer.Result) {
	for result := range results {
		peer := c.assigned[result.Index]
		peer.UnassignPiece(result.Index)

		if result.Err != nil {
			c.log.Write(
				"[FAILED TO DOWNLOAD]",
				"peer", peer.Addr,
				"piece", result.Index,
				"erorr", result.Err,
				"peers", len(c.peers),
			)
			c.unassign(result.Index)
		} else {
			c.log.Write(
				"[DOWNLOAD]",
				"peer", peer.Addr,
				"piece", result.Index,
				"piece_offset", result.Begin,
				"piece_length", result.LenBlock,
				"peers", len(c.peers),
			)
			// increment the number of downloaded
			// c.announcer.IncDownloaded(uint64(result.LenBlock))
			// update bitfield
			c.Bitfield.SetPiece(result.Index)
			// assign new pieces to peer
			c.schedulePieces(peer)
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

	// go c.announcer.Run(ctx)
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

			peer := peer.New(conn, c.info, writerC, c.log)

			peer.OnHandshake = func() {
				c.addPeer(peer)
			}
			peer.OnScehdulePieces = func() {
				c.log.Debug("peer needs pieces", "peer", peer.Addr)
				c.schedulePieces(peer)
			}
			peer.OnReassign = func(pieces []int) {
				c.log.Info("Peer is reassigning pieces back to scheduler",
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
		peers := c.peers
		for _, peer := range peers {
			peer.Print()
		}
	}
}
