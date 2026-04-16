package client

import (
	"context"
	"errors"
	"net"
	"time"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent"
	"github.com/Kostaaa1/bittorrent/internal/torrent/peer"
	"github.com/Kostaaa1/bittorrent/internal/torrent/shared"
	"github.com/Kostaaa1/bittorrent/internal/torrent/tracker"
)

var ErrNoPeers = errors.New("failed to assign pieces: 0 peers")
var ErrFailedAssignment = errors.New("failed to assign pieces: no peers can accept the pieces")

type Client struct {
	ID        [20]byte
	Port      uint16
	Bitfield  shared.Bitfield
	writer    *peer.PieceWriter
	announcer *tracker.Announcer
	info      *torrent.TorrentInfo
	log       *logger.Log
	// active peers which passed the handshake
	peerCh    chan tracker.PeerAddress
	scheduler *Scheduler
	// assigned             map[int]*peer.Peer
	// peers                []*peer.Peer
	// unassigned           map[int]struct{}
	// mu                   sync.Mutex
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
		ID:        clientID,
		Port:      port,
		Bitfield:  make([]byte, (len(pieces)+7)/8),
		writer:    writer,
		announcer: announcer,
		peerCh:    peerCh,
		info:      info,
		log:       log,
		scheduler: newScheduler(pieces),
		// maxConnectedPeers: 10,
	}
}

func (c *Client) collectResults(results <-chan peer.Result) {
	for result := range results {
		// peer := c.assigned[result.Index]
		// peer.UnassignPiece(result.Index)

		if result.Err != nil {
			c.log.Write(
				"[FAILED TO DOWNLOAD]",
				// "peer", peer.Addr,
				"piece", result.Index,
				"erorr", result.Err,
				// "peers", len(c.peers),
				// "endgame", c.endgameMode,
			)
			// c.unassign(result.Index)
		} else {
			c.log.Write(
				"[DOWNLOAD]",
				// "peer", peer.Addr,
				"piece", result.Index,
				"piece_offset", result.Begin,
				"piece_length", result.LenBlock,
				// "peers", len(c.peers),
				// "endgame", c.endgameMode,
			)
			// increment the number of downloaded
			// c.announcer.IncDownloaded(uint64(result.LenBlock))
			// update bitfield

			c.Bitfield.SetPiece(result.Index)

			// assign new pieces to peer
			// c.schedulePieces(peer)

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

			peer := peer.New(conn, c.info, writerC, c.log)

			// peer.OnHandshake = func() {
			// 	c.addPeer(peer)
			// }
			// peer.OnScehdulePieces = func() {
			// 	c.log.Debug("peer needs pieces", "peer", peer.Addr)
			// 	c.schedulePieces(peer)
			// }
			// peer.OnReassign = func(pieces []int) {
			// 	c.log.Info("Peer is reassigning pieces back to scheduler",
			// 		"peer", peer.Addr,
			// 		"free_pieces", pieces,
			// 	)
			// 	// TODO: currently adding pieces to unassigned, might be better to directly reassign them among other peers
			// 	for _, piece := range pieces {
			// 		c.unassign(piece)
			// 	}
			// }

			if err := peer.Open(ctx, hs, c.Bitfield); err != nil {
				c.log.Error("[PEER DISCONNECT]", "error: failed to read message", err)
				// c.removePeer(peer)
				return
			}
		}()
	}
}
