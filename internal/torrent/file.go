package torrent

import (
	"encoding/hex"
	"fmt"
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
	unassignedPieces  chan *Piece
	assigned          map[int]int
	blockSize         int
	numBlocksPerPiece int8
	writer            *PieceWriter
	results           chan Result
	peerAutoIncrement int
	peers             []*Peer
}

func (pm *PieceManager) assignPiece(peer *Peer) {
	// TODO: check if peer has the piece?
	piece := <-pm.unassignedPieces
	peer.assignedPiece = piece
	pm.assigned[piece.index] = peer.ID
	peer.pipeline.requested = 0
	fmt.Printf("[ASSIGN] piece index=%d, peer=%s\n", piece.index, peer.conn.RemoteAddr())
}

func (pm *PieceManager) unassignPiece(peer *Peer, piece *Piece) {
	piece.state = PieceMissing
	pm.unassignedPieces <- piece
	delete(pm.assigned, piece.index)
	peer.assignedPiece = nil
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

func (tf *TorrentFile) buildPieceMap() map[int]*Piece {
	pieces := make(map[int]*Piece)
	for id, hash := range tf.Pieces {
		size := tf.PieceLength
		lastPiece := len(tf.Pieces) - 1
		if id == lastPiece {
			size = tf.TotalLength - (lastPiece * tf.PieceLength)
		}
		pieces[id] = NewPiece(id, hash, size)
	}
	return pieces
}

func (tf *TorrentFile) Download(clientID [20]byte, port uint16) error {
	peerCh := make(chan tracker.PeerAddress)

	request := &tracker.AnnounceRequest{
		InfoHash: tf.InfoHash,
		PeerID:   clientID,
		Port:     port,
		Left:     tf.TotalLength,
	}

	hs := &Handshake{
		Pstr:      []byte("BitTorrent protocol"),
		Reserverd: [8]byte{},
		InfoHash:  tf.InfoHash,
		PeerID:    clientID,
	}

	blockSize := int(math.Pow(2, 14))
	numBlocksPerPiece := int8(tf.PieceLength / blockSize)

	pieces := tf.buildPieceMap()
	results := make(chan Result)

	writer := &PieceWriter{
		pieceLength:       tf.PieceLength,
		numBlocksPerPiece: numBlocksPerPiece,
		numWorkers:        3,
		worker:            make(chan PieceMessage),
		pieces:            pieces,
		files:             tf.Files,
		results:           results,
	}
	go writer.Start()

	var wg sync.WaitGroup

	pm := &PieceManager{
		// pieces:  pieces,
		// peers:   make([]*Peer, 0),
		unassignedPieces: make(chan *Piece),
		assigned:         make(map[int]int),
		writer:           writer,
		results:          results,
	}

	go func() {
		for _, piece := range pieces {
			pm.unassignedPieces <- piece
		}
	}()

	wg.Add(1)

	go func() {
		defer wg.Done()
		for {
			for result := range pm.results {
				if result.Err != nil {
					// piece write failed
					// unassign/reassign ?
					// state to missing/assigned
				} else {
					peerID := pm.assigned[result.Index]
					for _, peer := range pm.peers {
						if peer.ID == peerID {
							pm.assignPiece(peer)
							// piece := <-pm.unassignedPieces
							// peer.assignedPiece = piece
							// pm.assigned[piece.index] = peer.ID
							// peer.pipeline.requested = 0
						}
					}
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

	wg.Add(1)

	go func() {
		defer wg.Done()

		for p := range peerCh {
			go func() {
				sem <- struct{}{}
				defer func() { <-sem }()

				addr := p.IP4Addr()

				conn, err := net.DialTimeout("tcp", addr, time.Second*5)
				if err != nil {
					fmt.Printf("failed to dial: error=%v\n", err)
					return
				}

				peer := &Peer{conn: conn}

				if err := peer.initiateHandshake(hs); err != nil {
					fmt.Printf("failed to initiate handshake: error=%v\n", err)
					conn.Close()
					return
				}

				fmt.Println("Handshake success:", addr)

				peer.amInterested = false
				peer.peerChoking = true
				peer.totalLength = tf.TotalLength
				peer.blockSize = blockSize
				peer.numBlocksPerPiece = numBlocksPerPiece
				peer.pieceLength = tf.PieceLength
				peer.numOfPieces = len(tf.Pieces)
				peer.pipeline = NewPipeline(10, numBlocksPerPiece)
				peer.writer = writer.worker
				peer.ID = pm.peerAutoIncrement

				pm.peerAutoIncrement++

				piece := <-pm.unassignedPieces
				peer.assignedPiece = piece
				pm.assigned[piece.index] = peer.ID

				pm.peers = append(pm.peers, peer)

				if err := peer.sendInterested(); err != nil {
					fmt.Println(err)
					return
				}

				go peer.StartRequestPipeline()

				if err := peer.ReadMessages(); err != nil {
					fmt.Println("failed to read the messages:", err)
					return
				}
			}()
		}
	}()

	if err := tf.DiscoverPeers(peerCh, request); err != nil {
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
