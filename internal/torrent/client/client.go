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
	ID         [20]byte
	Port       uint16
	Bitfield   peer.Bitfield
	assigned   map[int]*peer.Peer
	unassigned map[int]struct{}
	writer     *peer.PieceWriter
	announcer  *tracker.Announcer
	info       *torrent.TorrentInfo
	log        *logger.Log
	// active peers which passed the handshake
	peers                []*peer.Peer
	peerCh               chan tracker.PeerAddress
	mu                   sync.Mutex
	maxGlobalConnections int
	maxConnectedPeers    int
	maxHalfOpen          int
	endgameMode          bool
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
	// 		IP:   "91.197.66.251",
	// 		Port: 51413,
	// 	}
	// 	peerCh <- tracker.PeerAddress{
	// 		IP:   "185.159.158.108",
	// 		Port: 60491,
	// 	}
	// 	peerCh <- tracker.PeerAddress{
	// 		IP:   "212.104.214.132",
	// 		Port: 52048,
	// 	}
	// 	peerCh <- tracker.PeerAddress{
	// 		IP:   "212.104.214.132",
	// 		Port: 52048,
	// 	}
	// }()

	go func() {
		peerCh <- tracker.PeerAddress{
			IP:   "148.56.177.88",
			Port: 14420,
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
		ID:                clientID,
		Port:              port,
		Bitfield:          make([]byte, (len(pieces)+7)/8),
		assigned:          make(map[int]*peer.Peer),
		unassigned:        unassigned,
		peers:             make([]*peer.Peer, 0),
		writer:            writer,
		announcer:         announcer,
		peerCh:            peerCh,
		info:              info,
		log:               log,
		maxConnectedPeers: 1,
		// maxConnectedPeers: 10,
	}
}

func (c *Client) addPeer(peer *peer.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.peers = append(c.peers, peer)
}

func (c *Client) removePeer(target *peer.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pieces := target.Assigned(); len(pieces) > 0 {
		c.peers = slices.DeleteFunc(c.peers, func(p *peer.Peer) bool {
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

func (c *Client) assign(target *peer.Peer, piece int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.unassigned, piece)
	c.assigned[piece] = target
}

func (c *Client) assignPieces(target *peer.Peer, pieces []int) {
	if len(pieces) == 0 {
		return
	}
	target.Assign(pieces)
	for _, piece := range pieces {
		c.assign(target, piece)
	}
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

	cap := target.Capacity()
	pieces := make([]int, 0, cap)
	n := 0

	c.mu.Lock()
	unassigned := c.unassigned
	c.log.Assignment("target is assignable",
		"target", target.Addr,
		"pieces", target.Assigned(),
		"missing", cap,
		"unassigned", len(c.unassigned),
	)
	c.mu.Unlock()

	if len(unassigned) > 0 {
		for piece := range unassigned {
			if target.CanAssignPiece(piece) {
				pieces = append(pieces, piece)
				n++
			}
			if cap == n {
				break
			}
		}
	} else {
		// TODO: could possibly take portion of pieces from other peers (choose them randomly or by their effectiveness)
		c.log.Debug("NO MORE IN UNASSIGNED",
			"target", target.Addr,
			"missing", cap,
			"pieces", target.Assigned(),
		)
	}

	c.assignPieces(target, pieces)
}

func (c *Client) collectResults(results <-chan peer.Result) {
	for result := range results {
		c.mu.Lock()
		peer := c.assigned[result.Index]
		c.mu.Unlock()

		peer.UnassignPiece(result.Index)

		c.mu.Lock()
		if c.info.NumOfPieces-len(c.assigned)+len(c.unassigned) <= 20 {
			c.endgameMode = true
		}
		c.mu.Unlock()

		if result.Err != nil {
			c.log.Write(
				"[FAILED TO DOWNLOAD]",
				"peer", peer.Addr,
				"piece", result.Index,
				"erorr", result.Err,
				"peers", len(c.peers),
				"endgame", c.endgameMode,
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
				"endgame", c.endgameMode,
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
				// TODO: currently adding pieces to unassigned, might be better to directly reassign them among other peers
				for _, piece := range pieces {
					c.unassign(piece)
				}
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
