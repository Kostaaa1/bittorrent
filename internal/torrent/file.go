package torrent

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"sync"
	"test/internal/torrent/tracker"
	"test/pkg/bencode"
	"time"
)

type Result struct {
	Index int
	Begin int
	Err   error
}

type PieceManager struct {
	failedPieces      chan int
	assigned          map[int]int // TODO: piece ID -> peer ID instead
	numBlocksPerPiece int8
	writer            *PieceWriter
	results           chan Result
	peerAutoIncrement int
	peer              []*Peer
}

// Problem: Peers needs to assign pieces for themselves, so when peer assign the piece, it should notify the manager that that piece is being reserved by peer

// Solution 1:
// We need to ditch the logic of piece manager to assign pieces to peers on successfully downloaded pieces. Only assign to peers when the download fails.

// before peer assigns the next piece that it has, check if its reserved or downloaded previously by other peer. Iterate until it finds the next available piece?

// notify piece manager (via channel) every time peer assigns new piece to itself, use map[pieceIndex]peerIndex for assigned pieces in piece manager.

// when download of piece fails for some reason, then:
// MARK PIECE MISSING - delete (or mark that failed) in map - means that piece is missing
// find the peer that has the piece
// send piece index to peer individual queue

// MUST NOT FAIL, ALWAYS NEEDS TO ASSIGN SOME PIECE
func (pm *PieceManager) assignPiece(peer *Peer) {
	// pieceIndex := <-pm.unassignedPieces
	// ok := peer.AssignPiece(pieceIndex)
	// if !ok {
	// 	pm.unassignedPieces <- pieceIndex
	// 	pm.assignPiece(peer)
	// 	return
	// }
	// pm.assigned[pieceIndex] = peer
}

type FileEntry struct {
	ID          int
	file        *os.File
	FullPath    string
	Length      int
	StartOffset int
	EndOffset   int
}

type TorrentFile struct {
	TotalLength int
	PieceLength int
	Announce    string
	UrlList     []string
	Files       []*FileEntry
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

	hs := &Handshake{
		Pstr:      []byte("BitTorrent protocol"),
		Reserverd: [8]byte{},
		InfoHash:  tf.InfoHash,
		PeerID:    clientID,
	}

	blockSize := int(math.Pow(2, 14))
	numBlocksPerPiece := int8(tf.PieceLength / blockSize)

	results := make(chan Result)

	writer := &PieceWriter{
		worker:            make(chan PieceMessage),
		pieces:            make(map[int]*PieceBuffer),
		files:             tf.Files,
		results:           results,
		pieceLength:       tf.PieceLength,
		numBlocksPerPiece: numBlocksPerPiece,
		numOfPieces:       len(tf.Pieces),
		totalLength:       tf.TotalLength,
		hashPieces:        tf.Pieces,
	}

	go writer.Start()

	var wg sync.WaitGroup

	pm := &PieceManager{
		unassignedPieces: make(chan int),
		assigned:         make(map[int]int),
		writer:           writer,
		results:          results,
	}

	logger := newLogger()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for index := range tf.Pieces {
			pm.unassignedPieces <- index
		}
		fmt.Println("exit from unassigned pieces")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			for result := range pm.results {
				if result.Err != nil {
					// piece write failed
					// unassign/reassign?
					logger.Error("[FAIL DOWNLOAD]", "piece", result.Index, "error", result.Err)
				} else {
					logger.Debug("[DOWNLOADED]", "piece", result.Index)
					// peer := pm.assigned[result.Index]
					// pm.assignPiece(peer)
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

	// print peers
	// peerTicker := time.NewTicker(10 * time.Second)
	// go func() {
	// 	for range peerTicker.C {
	// 		fmt.Println("Connected to peers:")
	// 		for _, p := range pm.peer {
	// 			fmt.Println("Peer:", p.conn.RemoteAddr())
	// 		}
	// 	}
	// }()

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

				peer := &Peer{conn: conn}

				if err := peer.initiateHandshake(hs); err != nil {
					logger.Error("[HANDSHAKE]", "status", "failed", "error", err)
					conn.Close()
					return
				}

				logger.Info("[HANDSHAKE]", "status", "success", "peer", addr)

				// debugg
				pm.peer = append(pm.peer, peer)

				peer.amInterested = false
				peer.peerChoking = true
				peer.totalLength = tf.TotalLength
				peer.blockSize = blockSize
				peer.numBlocksPerPiece = numBlocksPerPiece
				peer.pieceLength = tf.PieceLength
				peer.numOfPieces = len(tf.Pieces)
				peer.pipeline = NewPipeline(5)
				peer.writer = writer.worker
				peer.ID = pm.peerAutoIncrement
				peer.log = logger
				pm.peerAutoIncrement++
				pm.assignPiece(peer)

				if err := peer.ReadMessages(); err != nil {
					logger.Error("[PEER]", "error: failed to read message", err)
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
