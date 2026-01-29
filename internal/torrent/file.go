package torrent

import (
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"test/internal/torrent/tracker"
	"test/pkg/bencode"
)

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

	// for printing...
	AnnounceList [][]string
	Comment      string
	CreatedBy    string
	CreationDate int64
	Encoding     string
	Publisher    string
	PublisherURL string
	Private      int
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
	if len(b.AnnounceList) > 0 {
		fmt.Println("")
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
		fmt.Println("")
		fmt.Println("TRACKERS")
		fmt.Println("")
		fmt.Println(prefix, "Tier #1")
		fmt.Println(prefix, b.Announce)
	}
	if len(b.UrlList) > 0 && b.UrlList[0] != "" {
		fmt.Println("")
		fmt.Println("WEBSEEDS")
		fmt.Println("")
		for _, url := range b.UrlList {
			fmt.Println(prefix, url)
		}
	}
	if len(b.Files) > 0 {
		fmt.Println("FILES")
		fmt.Println("")
		for _, f := range b.Files {
			fmt.Printf("%s%s (%d)\n", prefix, f.FullPath, f.Length)
		}
	}
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

func (tf *TorrentFile) Download(clientID [20]byte, port uint16) error {
	p := tracker.HTTPTrackerRequestParams{
		Tracker:  tf.Announce,
		InfoHash: tf.InfoHash,
		PeerID:   clientID,
		Port:     port,
		Left:     tf.TotalLength,
	}

	trackerURL, err := tracker.BuildHTTPTrackerURL(p)
	if err != nil {
		return err
	}

	peers, err := tracker.RequestPeers(trackerURL)
	if err != nil {
		return err
	}

	fmt.Println(tf.Announce)
	fmt.Println("Peers:", peers)

	hs := &Handshake{
		Pstr:      []byte("BitTorrent protocol"),
		Reserverd: [8]byte{},
		InfoHash:  tf.InfoHash,
		PeerID:    clientID,
	}

	var wg sync.WaitGroup

	for _, peer := range peers {
		p := peer
		wg.Add(1)
		go func() {
			defer wg.Done()

			ip4 := fmt.Sprintf("%s:%d", p.IP, p.Port)

			if err := DialPeer(ip4, hs, tf); err != nil {
				fmt.Println("Failed to dial peer:", err)
				return
			}
		}()
	}

	wg.Wait()

	return nil
}
