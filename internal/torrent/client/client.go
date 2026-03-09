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

type Client struct {
	ID       [20]byte
	Port     uint16
	Bitfield peer.Bitfield

	assigned   map[int]uint64
	unassigned map[int]struct{}

	writer    *peer.PieceWriter
	announcer *tracker.Announcer
	info      *torrent.TorrentInfo

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
		// maxConnectedPeers: 2,
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
	delete(c.assigned, piece)
	c.unassigned[piece] = struct{}{}
	c.mu.Unlock()
}

func (c *Client) assign(peer *peer.Peer, piece int) bool {
	if !peer.CanAssign() {
		return false
	}

	peer.AddPieceToQueue(piece)

	delete(c.unassigned, piece)
	c.assigned[piece] = peer.ID

	return true
}

func (c *Client) unassignPieces(pieces []int) {
	for _, piece := range pieces {
		c.unassign(piece)
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

func (c *Client) fillPipelineFromPeers(target *peer.Peer) {
	// get the amount that peer can accept
	// find slowest peer and reassign its pieces to the target
	// assign pieces as long as target can accept and available peers
}

func (c *Client) fillPipelineFromUnassigned(target *peer.Peer) {
}

// Fills peers request pipeline as long as peer can accept the new piece to request.
// assign pieces from map if there are any
// otherwise, take portion of pieces from other peers
func (c *Client) fillPeerPipeline(target *peer.Peer) {
	if !target.CanAssign() {
		return
	}

	for target.CanAssign() {
		var pieceID int
		var found bool

		c.mu.Lock()
		for id := range c.unassigned {
			pieceID = id
			found = true
			break
		}
		c.mu.Unlock()

		if !found {
			// Ask other peers for portion of their pieces and then reassign it to target
			c.log.Info("[UNASSIGNING FROM OTHER PEERS]")

			// for _, peer := range c.peers {
			// 	if !target.CanAssign() {
			// 		break
			// 	}
			// 	if peer.ID != target.ID {
			// 		newpieces := peer.ReassignPieces()
			// 		unassign := make([]int, 0)
			// 		c.log.Debug("[REASSIGNING PIECES]", "pieces", newpieces)
			// 		for _, piece := range newpieces {
			// 			if !target.HasPiece(piece) {
			// 				continue
			// 			}
			// 			if assigned := c.assign(target, piece); assigned {
			// 				c.log.Debug("I GUESS ASSIGNIGN", peer.Addr, piece)
			// 				unassign = append(unassign, piece)
			// 			}
			// 		}
			// 		if len(unassign) > 0 {
			// 			for _, piece := range unassign {
			// 				peer.UnassignPiece(piece)
			// 			}
			// 		}
			// 	}
			// }
			break
		}

		if !target.HasPiece(pieceID) {
			continue
		}

		c.mu.Lock()
		if _, ok := c.unassigned[pieceID]; ok {
			c.assign(target, pieceID)
		}
		c.mu.Unlock()
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
				c.fillPeerPipeline(peer)
			}
			peer.OnUnassign = func(pieces []int) {
				c.log.Info("OnUnassign", "free_pieces", pieces)
				c.unassignPieces(pieces)
				// if err := c.rearrangePieces(peer, pieces); err != nil {
				// 	panic(err)
				// }
			}
			peer.OnMissingPiece = func() {
				c.log.Info("OnMissing PIECE")
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

// func (c *Client) printPeers() {
// 	ticker := time.NewTicker(time.Second * 30)
// 	for range ticker.C {
// 		c.mu.Lock()
// 		peers := c.peers
// 		fmt.Println("ACTIVE PEER CONNECTIONS", len(peers))
// 		for _, peer := range peers {
// 			peer.Print()
// 		}
// 		c.mu.Unlock()
// 	}
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
