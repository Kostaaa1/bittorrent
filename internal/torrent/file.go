package torrent

import (
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"math"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"test/internal/torrent/io"
	"test/internal/torrent/peer"
	"test/internal/torrent/tracker"
	"test/pkg/bencode"
	"time"
)

type PieceManager struct {
	bitfield   peer.Bitfield
	mu         sync.Mutex
	assigned   map[int]uint64
	unassigned map[int]uint64
	peers      []*peer.Peer
}

func NewPieceManager(pieces [][20]byte) *PieceManager {
	// TODO: do this via channel in separate goroutine...
	unassigned := make(map[int]uint64, len(pieces))
	for index := range pieces {
		unassigned[index] = 0
	}
	return &PieceManager{
		assigned:   make(map[int]uint64),
		unassigned: unassigned,
		peers:      make([]*peer.Peer, 0),
	}
}

func (pm *PieceManager) addPeer(peer *peer.Peer) {
	pm.mu.Lock()
	pm.peers = append(pm.peers, peer)
	pm.mu.Unlock()
}

func (pm *PieceManager) getAssignedPeer(pieceIndex int) *peer.Peer {
	pm.mu.Lock()
	peerID, found := pm.assigned[pieceIndex]
	fmt.Println("PIECE ASSIGNED TO PEER", pieceIndex, peerID)
	pm.mu.Unlock()

	if !found {
		return nil
	}

	return pm.peers[peerID]
}

func (pm *PieceManager) unassign(pieceIndex int) {
	pm.mu.Lock()
	delete(pm.assigned, pieceIndex)
	pm.unassigned[pieceIndex] = 0
	pm.mu.Unlock()
}

func (pm *PieceManager) fillPeerQueue(peer *peer.Peer) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for peer.CanAssign() {
		assigned := false
		for pieceID := range pm.unassigned {
			if peer.HasPiece(pieceID) {
				delete(pm.unassigned, pieceID)
				pm.assigned[pieceID] = peer.ID

				peer.AddPieceToQueue(pieceID)

				assigned = true
				break
			}
		}
		if !assigned {
			break
		}
	}
}

type TorrentFile struct {
	TotalLength int
	PieceLength int
	Announce    string
	UrlList     []string
	Files       []*io.FileEntry
	Name        string
	Pieces      [][20]byte
	InfoHash    [20]byte

	AnnounceList [][]string
	Comment      string
	CreatedBy    string
	CreationDate int64
	Encoding     string
	Publisher    string
	PublisherURL string
	Private      int
}

func NewFile(filename string) (*TorrentFile, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var src bencodeTorrent
	if err := bencode.NewDecoder(f).Decode(&src); err != nil {
		return nil, err
	}

	return src.toTorrentFile()
}

func (tf *TorrentFile) DiscoverPeers(
	c chan<- tracker.PeerAddress,
	req *tracker.AnnounceRequest,
) error {
	// c <- tracker.PeerAddress{IP: "185.149.91.39", Port: 20046}
	// return nil

	if len(tf.AnnounceList) > 0 {
		for _, list := range tf.AnnounceList {
			for _, ann := range list {
				req.Tracker = ann
				peers, err := tracker.DiscoverPeers(*req)
				if err != nil {
					fmt.Printf("failed to discover peers: announce=%s, error=%v\n", ann, err)
					continue
				}
				for _, peer := range peers {
					c <- peer
				}
			}
		}
	} else {
		req.Tracker = tf.Announce
		peers, err := tracker.DiscoverPeers(*req)
		if err != nil {
			return err
		}
		for _, peer := range peers {
			c <- peer
		}
	}
	return nil
}

func newLogger() *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey && len(groups) == 0 {
				return slog.Attr{}
			}
			return a
		},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, opts))
	slog.SetDefault(logger)
	return logger
}

func (tf *TorrentFile) Download(clientID [20]byte, port uint16) error {
	peerCh := make(chan tracker.PeerAddress)

	hs := peer.Handshake{
		Pstr:      []byte("BitTorrent protocol"),
		Reserverd: [8]byte{},
		InfoHash:  tf.InfoHash,
		PeerID:    clientID,
	}

	blockSize := int(math.Pow(2, 14))
	numBlocksPerPiece := tf.PieceLength / blockSize

	writer := io.NewPieceWriter(
		tf.PieceLength,
		tf.Pieces,
		tf.Files,
		tf.TotalLength,
		numBlocksPerPiece,
	)
	writerC, resultC := writer.Channles()
	go writer.Start()

	pm := NewPieceManager(tf.Pieces)
	logger := newLogger()

	var wg sync.WaitGroup

	f, err := os.OpenFile("log.txt", os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	writerLog := log.New(f, "", log.Ldate|log.Ltime)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			for result := range resultC {
				if result.Err != nil {
					logger.Error("[FAIL DOWNLOAD]", "piece", result.Index, "error", result.Err)
					pm.unassign(result.Index)
				} else {
					peer := pm.getAssignedPeer(result.Index)
					logger.Debug("[DOWNLOADED]", "piece", result.Index, "peer_addr", peer.Addr, "peer_id", peer.ID, "num_of_peers", len(pm.peers), "left_unassigned", len(pm.unassigned), "len_block", result.LenBlock)

					writerLog.Println("[DOWNLOADED]", "piece", result.Index, "peer_addr", peer.Addr, "num_of_peers", len(pm.peers), "offset", result.Begin, "block_size", result.LenBlock)

					pm.fillPeerQueue(peer)
				}
			}
		}
	}()

	sem := make(chan struct{}, 10)

	tick := time.NewTicker(3 * time.Second)
	go func() {
		for range tick.C {
			<-sem
		}
	}()

	var peerCounter uint64 = 0

	wg.Add(1)
	go func() {
		defer wg.Done()

		for p := range peerCh {
			go func() {
				sem <- struct{}{}
				defer func() { <-sem }()

				addr := p.IP4Addr()

				conn, err := net.DialTimeout("tcp", addr, time.Second*15)
				if err != nil {
					logger.Error("failed to dial", "error", err)
					return
				}

				id := atomic.AddUint64(&peerCounter, 1) - 1

				peer := peer.New(id, conn, writerC, logger)
				peer.SetInfo(
					len(tf.Pieces),
					tf.TotalLength,
					tf.PieceLength,
					blockSize,
					numBlocksPerPiece,
				)
				pm.addPeer(peer)
				peer.OnUnchoke = func() {
					pm.fillPeerQueue(peer)
				}
				peer.OnChoke = func(pieces []int) {
					for _, piece := range pieces {
						pm.unassign(piece)
					}
				}

				if err := peer.Open(hs); err != nil {
					logger.Error("[PEER DISCONNECT]", "error: failed to read message", err)
					return
				}
			}()
		}
	}()

	annReq := &tracker.AnnounceRequest{
		InfoHash: tf.InfoHash,
		PeerID:   clientID,
		Port:     port,
		Left:     tf.TotalLength,
	}

	if err := tf.DiscoverPeers(peerCh, annReq); err != nil {
		return err
	}

	wg.Wait()

	return nil
}

func (b *TorrentFile) Print() {
	prefix := "  "
	fmt.Println("Name:", b.Name)
	fmt.Println("")
	fmt.Println("GENERAL")
	fmt.Println("")
	fmt.Println(prefix, "Name:", b.Name)
	fmt.Println(prefix, "Hash:", hex.EncodeToString(b.InfoHash[:]))
	if b.CreatedBy != "" {
		fmt.Println(prefix, "Created by:", b.CreatedBy)
	}
	if b.CreationDate > 0 {
		fmt.Println(prefix, "Created on:", b.CreationDate)
	}
	fmt.Println("")
	if b.Comment != "" {
		fmt.Println(prefix, "Comment:", b.Comment)
	}
	if b.Publisher != "" {
		fmt.Println(prefix, "Source:", b.Publisher)
	}
	fmt.Println(prefix, "Piece Count:", len(b.Pieces))
	fmt.Println(prefix, "Piece Size:", b.PieceLength)
	fmt.Println(prefix, "Total Size:", b.TotalLength)
	if b.Private == 0 {
		fmt.Println(prefix, "Privacy: Public torrent")
	} else {
		fmt.Println(prefix, "Privacy: Private torrent")
	}
	fmt.Println("")
	if len(b.AnnounceList) > 0 {
		fmt.Println("TRACKERS")
		fmt.Println("")
		for i, ann := range b.AnnounceList {
			fmt.Println(prefix, "Tier #", i+1)
			for _, t := range ann {
				fmt.Println(prefix, t)
			}
			fmt.Println("")
		}
	} else if b.Announce != "" {
		fmt.Println("TRACKERS")
		fmt.Println("")
		fmt.Println(prefix, "Tier #1")
		fmt.Println(prefix, b.Announce)
		fmt.Println("")
	}
	if len(b.UrlList) > 0 && b.UrlList[0] != "" {
		fmt.Println("WEBSEEDS")
		fmt.Println("")
		for _, url := range b.UrlList {
			fmt.Println(prefix, url)
		}
		fmt.Println("")
	}
	if len(b.Files) > 0 {
		fmt.Println("FILES")
		fmt.Println("")
		for _, f := range b.Files {
			fmt.Printf("%s%s (%d)\n", prefix, f.FullPath, f.Length)
		}
	}
	fmt.Println("")
}
