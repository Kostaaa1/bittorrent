package client

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"time"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent"
	"github.com/Kostaaa1/bittorrent/internal/torrent/peer"
	"github.com/Kostaaa1/bittorrent/internal/torrent/scheduler"
	"github.com/Kostaaa1/bittorrent/internal/torrent/shared"
	"github.com/Kostaaa1/bittorrent/internal/torrent/tracker"
)

var ErrNoPeers = errors.New("failed to assign pieces: 0 peers")
var ErrFailedAssignment = errors.New("failed to assign pieces: no peers can accept the pieces")

type Client struct {
	ID                   [20]byte
	Port                 uint16
	Bitfield             shared.Bitfield
	writer               *peer.PieceWriter
	peerCh               <-chan tracker.PeerAddress
	announcer            *tracker.Announcer
	info                 *torrent.TorrentInfo
	scheduler            *scheduler.Scheduler
	log                  *logger.Log
	maxGlobalConnections int
	maxConnectedPeers    int
	maxHalfOpen          int
}

func (c *Client) Handshake() peer.Handshake {
	return peer.Handshake{
		Pstr:      []byte("BitTorrent protocol"),
		Reserverd: [8]byte{},
		PeerID:    c.ID,
		InfoHash:  c.info.InfoHash,
	}
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
	peerCh := make(chan tracker.PeerAddress)

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
		announcer:         announcer,
		peerCh:            peerCh,
		info:              info,
		log:               log,
		writer:            peer.NewPieceWriter(info, pieces, files, log),
		scheduler:         scheduler.New(pieces, log),
		maxConnectedPeers: 1,
		// maxGlobalConnections: ,
		// maxHalfOpen: ,
	}
}

func (c *Client) collectResults(results <-chan peer.Result) {
	c.log.Write("[RESULTS] collector started", "num_pieces", c.info.NumOfPieces)
	defer c.log.Write("[RESULTS] collector stopped: results channel closed")

	var ok, failed int

	for result := range results {
		// peer := c.assigned[result.Index]
		// peer.UnassignPiece(result.Index)

		if result.Err != nil {
			failed++
			c.log.Write(
				"[FAILED TO DOWNLOAD]",
				// "peer", peer.Addr,
				"piece", result.Index,
				"erorr", result.Err,
				"ok_total", ok,
				"failed_total", failed,
				"num_pieces", c.info.NumOfPieces,
				// "peers", len(c.peers),
				// "endgame", c.endgameMode,
			)
			// c.unassign(result.Index)
		} else {
			ok++
			c.log.Write(
				"[DOWNLOAD]",
				// "peer", peer.Addr,
				"piece", result.Index,
				"piece_offset", result.Begin,
				"piece_length", result.LenBlock,
				"ok_total", ok,
				"failed_total", failed,
				"num_pieces", c.info.NumOfPieces,
				"remaining", c.info.NumOfPieces-ok,
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
	eventCh := c.scheduler.Channel()
	writerCh, resultCh := c.writer.Channels()

	// announcer discovers peers and announces us to other trackers
	go c.announcer.Run(ctx)
	// writer receives blocks at writerC and sends results when piece gets downloaded to resultC
	go c.writer.Run()
	// receive results (pieces that can succeed/fail) from writer
	go c.collectResults(resultCh)
	// scheduler schedules pieces to the pool of peers
	go c.scheduler.Run(ctx)

	var peerSem chan struct{}
	if c.maxConnectedPeers > 0 {
		peerSem = make(chan struct{}, c.maxConnectedPeers)
	}

	c.log.Info("[CLIENT] run: goroutines started",
		"max_connected_peers", c.maxConnectedPeers,
		"num_pieces", c.info.NumOfPieces,
		"piece_length", c.info.PieceLength,
		"total_length", c.info.TotalLength,
	)

	var active atomic.Int64

	for p := range c.peerCh {
		addr := p.IP4Addr()
		c.log.Info("[CLIENT] peer discovered", "addr", addr, "active_peers", active.Load())

		go func() {
			if c.maxConnectedPeers > 0 {
				c.log.Debug(
					"[CLIENT] waiting for peer slot",
					"addr", addr,
					"active_peers", active.Load(),
					"limit", c.maxConnectedPeers,
				)
				peerSem <- struct{}{}
				defer func() { <-peerSem }()
			}

			n := active.Add(1)
			defer func() { c.log.Info("[CLIENT] peer goroutine done", "addr", addr, "active_peers", active.Add(-1)) }()

			c.log.Debug("[CLIENT] dialing", "addr", addr, "active_peers", n)

			conn, err := net.DialTimeout("tcp", addr, time.Second*5)
			if err != nil {
				c.log.Error("failed to dial", "addr", addr, "error", err)
				return
			}

			c.log.Info(
				"[CLIENT] dial ok",
				"addr", addr,
				"local", conn.LocalAddr().String(),
			)

			peer := peer.New(
				conn,
				c.info,
				writerCh,
				eventCh,
				c.log,
			)

			if err := peer.Listen(ctx, c.Handshake(), c.Bitfield); err != nil {
				c.log.Error("[PEER DISCONNECT]", "addr", addr, "error: failed to read message", err)
				return
			}

			c.log.Info("[PEER DISCONNECT] listen returned cleanly", "addr", addr)
		}()
	}

	c.log.Info("[CLIENT] run: peerCh closed, no longer accepting peers", "active_peers", active.Load())
}
