package client

import (
	"context"
	"errors"
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
	log                  *slog.Logger
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
		ID:                clientID,
		Port:              port,
		Bitfield:          make([]byte, (len(pieces)+7)/8),
		assigned:          make(map[int]uint64),
		unassigned:        unassigned,
		peers:             make([]*peer.Peer, 0),
		writer:            writer,
		announcer:         announcer,
		peerCh:            peerCh,
		peerCounter:       0,
		info:              info,
		log:               log,
		maxConnectedPeers: 3,
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

	if pieces := target.AssignedPieces(); len(pieces) > 0 {
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

func (c *Client) assign(peer *peer.Peer, piece int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if assigned := peer.Assign(piece); !assigned {
		return false
	}
	delete(c.unassigned, piece)
	c.assigned[piece] = peer.ID
	return true
}

// WIP:
func (c *Client) fillPeerPipeline(target *peer.Peer) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.unassigned) > 0 {
		for piece := range c.unassigned {
			if !target.CanAssign() {
				break
			}
			if assigned := c.assign(target, piece); !assigned {
				// panic("failed to assign")
				continue
			}
		}
	} else {
		c.log.Debug("[UNASSIGNING FROM OTHER PEERS]")
		for _, peer := range c.peers {
			assigned := peer.AssignedPieces()
			if len(assigned) == 0 {
				continue
			}

			for _, piece := range assigned {
				if !target.CanAssign() {
					break
				}
				if assigned := target.Assign(piece); !assigned {
					continue
				}
				peer.UnassignPiece(piece)
			}
		}
		assigned := target.AssignedPieces()
		c.log.Debug("AFTER ASSIGNMENT TARGET", "pieces", assigned)
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
				c.log.Debug("on Unchoke, assigning pieces")
				c.fillPeerPipeline(peer)
			}
			peer.OnMissingPiece = func() {
				c.log.Info("OnMissing PIECE")
				c.fillPeerPipeline(peer)
			}
			peer.OnUnassign = func(pieces []int) {
				c.log.Info("OnUnassign", "free_pieces", pieces)
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
