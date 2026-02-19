package torrent

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"test/internal/torrent/client"
	"test/internal/torrent/io"
	"test/internal/torrent/peer"
	"test/internal/torrent/tracker"
	"test/pkg/bencode"
	"time"
)

type TorrentFile struct {
	TotalLength  int
	PieceLength  int
	Announce     string
	UrlList      []string
	Files        []*io.FileEntry
	Name         string
	Pieces       [][20]byte
	InfoHash     [20]byte
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
	logger := slog.New(slog.NewTextHandler(os.Stdout, opts))
	slog.SetDefault(logger)
	return logger
}

func (tf *TorrentFile) Download(ctx context.Context, clientID [20]byte, port uint16) error {
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
		blockSize,
	)

	logger := newLogger()
	writerC, resultC := writer.Channles()
	client := client.New(logger, tf.Pieces)
	var peerCounter uint64 = 0
	// TODO: make it buffered?
	peerCh := make(chan tracker.PeerAddress)

	announcer := tracker.NewAnnouncer(
		tf.InfoHash,
		clientID,
		port,
		uint64(tf.TotalLength),
		peerCh,
		tf.Announce,
		tf.AnnounceList,
		true,
	)

	go announcer.Run(ctx)
	go writer.Start()
	go client.CollectResults(announcer, logger, resultC)

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		for p := range peerCh {
			go func() {
				conn, err := net.DialTimeout("tcp", p.IP4Addr(), time.Second*5)
				if err != nil {
					logger.Error("failed to dial", "error", err)
					return
				}

				peerID := atomic.AddUint64(&peerCounter, 1) - 1

				peer := peer.New(peerID, conn, writerC, logger)
				peer.SetInfo(
					len(tf.Pieces),
					tf.TotalLength,
					tf.PieceLength,
					blockSize,
					numBlocksPerPiece,
				)
				peer.OnUnchoke = func() {
					client.FillPeerQueue(peer)
				}
				peer.OnChoke = func(pieces []int) {
					logger.Info("OnChoke ran", "free_pieces", pieces)
					for _, piece := range pieces {
						client.FreePiece(piece)
					}
				}
				peer.OnHandshake = func() {
					client.AddPeer(peer)
				}
				if err := peer.Open(ctx, hs, client.Bitfield); err != nil {
					logger.Error("[PEER DISCONNECT]", "error: failed to read message", err)
					client.RemovePeer(peer)
					return
				}
			}()
		}
	}()

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
