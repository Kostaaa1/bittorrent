package torrent

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
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
	Files       []*FileEntry
	Name        string
	Pieces      [][20]byte
	InfoHash    [20]byte
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

func (tf *TorrentFile) Print() {
	fmt.Println("Name: ", tf.Name)
	fmt.Println("")
	fmt.Println("GENERAL")
	fmt.Println("")
	fmt.Println("  Name: ", tf.Name)
	fmt.Println("  Hash: ", hex.EncodeToString(tf.InfoHash[:]))
	fmt.Println("  Piece Count: ", len(tf.Pieces))
	fmt.Println("  Piece Size: ", tf.PieceLength)
	fmt.Println("  Total Size: ", tf.TotalLength)
	fmt.Println("  Privacy: ", hex.EncodeToString(tf.InfoHash[:]))
	fmt.Println("")
	fmt.Println("TRACKERS")
	fmt.Println("")
	fmt.Println("Print announce list...")
	fmt.Println("")
	fmt.Println("FILES")
	fmt.Println("")
	for _, f := range tf.Files {
		fmt.Println("  ", f.FullPath)
	}
}

func (tf *TorrentFile) Download(clientID [20]byte, port uint16) error {
	tf.Print()

	peers, err := tf.discoverPeers(clientID, port)
	if err != nil {
		return err
	}

	fmt.Println("Peers:", peers)

	hs := &Handshake{
		Pstr:      []byte("BitTorrent protocol"),
		Reserverd: [8]byte{},
		InfoHash:  tf.InfoHash,
		PeerID:    clientID,
	}

	var wg sync.WaitGroup
	semCh := make(chan struct{}, 5)

	for _, peer := range peers {
		wg.Add(1)
		go func() {
			defer func() {
				wg.Done()
				<-semCh
			}()

			semCh <- struct{}{}

			c, err := DialPeer(peer.ip4addr(), hs, tf)
			if err != nil {
				fmt.Println("handshake error:", err)
				return
			}
			_ = c

		}()
	}

	wg.Wait()

	// for _, peer := range peers {
	// 	if err := DialPeer(peer.ip4addr(), hs, tf); err != nil {
	// 		fmt.Printf("Peer dialing failed: peer=%s, error=%v\n", peer.ip4addr(), err)
	// 		continue
	// 	}
	// }

	// peer := bencodePeer{IP: "46.165.50.78", Port: 50025}
	// if _, err := DialPeer(peer.ip4addr(), hs, tf); err != nil {
	// 	fmt.Println("handshake error:", err)
	// 	return err
	// }

	return nil
}

func (tf *TorrentFile) buildHttpTrackerURL(peerID [20]byte, port uint16) (*url.URL, error) {
	parsed, err := url.Parse(tf.Announce)
	if err != nil {
		return nil, err
	}
	v := url.Values{
		"info_hash":  []string{string(tf.InfoHash[:])},
		"peer_id":    []string{string(peerID[:])},
		"port":       []string{strconv.Itoa(int(port))},
		"uploaded":   []string{"0"},
		"downloaded": []string{"0"},
		"left":       []string{strconv.Itoa(int(tf.TotalLength))},
		// "compact":    []string{"1"},
	}
	parsed.RawQuery = v.Encode()

	return parsed, nil
}

func toPeer(b [6]byte) bencodePeer {
	return bencodePeer{
		PeerID: "",
		IP:     fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3]),
		Port:   binary.BigEndian.Uint16([]byte{b[4], b[5]}),
	}
}

func parsePeersBinary(peers []byte) ([]bencodePeer, error) {
	if len(peers)%6 != 0 {
		return nil, fmt.Errorf("peers received in wrong format: not divisible by 6 - %d", len(peers))
	}

	numPeers := len(peers) / 6

	parsed := make([]bencodePeer, numPeers)
	for i := range parsed {
		v := [6]byte{}
		copy(v[:], peers[i:i+6])
		parsed[i] = toPeer(v)
	}

	return parsed, nil
}

func (tf *TorrentFile) discoverPeers(peerID [20]byte, port uint16) ([]bencodePeer, error) {
	trackerURL, err := tf.buildHttpTrackerURL(peerID, port)
	if err != nil {
		return nil, err
	}

	resp, err := http.Get(trackerURL.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res bencodeTrackerResponse
	if err := bencode.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	if res.FailureReason != "" {
		return nil, fmt.Errorf("failed to get the peers from HTTP tracker=%s, error=%s", trackerURL, res.FailureReason)
	}

	p := res.Peers.List
	if len(res.Peers.Compact) > 0 {
		v, err := parsePeersBinary(res.Peers.Compact)
		if err != nil {
		}
		p = v
	}

	return p, nil
}
