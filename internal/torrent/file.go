package torrent

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"test/pkg/bencode"
)

// type File struct {
// 	W        *os.File
// 	FullPath string
// 	Length   int
// }

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

func (tf *TorrentFile) Download(clientID [20]byte, port uint16) error {
	fmt.Println("Downloading file:")
	fmt.Println("	- Name:", tf.Name)
	fmt.Println("	- Files:", tf.Files)
	fmt.Println("	- Length:", tf.TotalLength)
	fmt.Println("	- PieceLength:", tf.PieceLength)
	fmt.Println("	- Pieces:", len(tf.Pieces))

	// peers, err := tf.discoverPeers(clientID, port)
	// if err != nil {
	// 	return err
	// }
	// fmt.Println(peers)

	hs := &Handshake{
		Pstr:      []byte("BitTorrent protocol"),
		Reserverd: [8]byte{},
		InfoHash:  tf.InfoHash,
		PeerID:    clientID,
	}

	// peer := bencodePeer{IP: "185.203.56.65", Port: 55734 }
	// peer := bencodePeer{IP: "82.65.106.14", Port: 6882}
	// peer := bencodePeer{IP: "193.105.133.241", Port: 8999}
	// peer := bencodePeer{IP: "143.177.152.79", Port: 2379}
	// peer := bencodePeer{IP: "174.106.249.177", Port: 12881}
	// peer := bencodePeer{IP: "91.193.6.186", Port: 48274}
	// peer := bencodePeer{IP: "136.62.0.15", Port: 17604}
	// peer := bencodePeer{IP: "198.54.134.252", Port: 11341 }
	// peer := bencodePeer{IP: "204.8.98.45", Port: 32030}
	// peer := bencodePeer{IP: "185.18.148.138", Port: 51413}
	// peer := bencodePeer{IP: "86.83.93.76", Port: 6881}
	peer := bencodePeer{IP: "216.81.9.154", Port: 47980}

	if err := DialPeer(peer.ip4addr(), hs, tf); err != nil {
		fmt.Println("handshake error:", err)
		return err
	}

	// var wg sync.WaitGroup
	// for _, peer := range peers {
	// 	wg.Add(1)
	// 	go func() {
	// 		defer wg.Done()
	// 		if err := peer.DialWithHandshake(hs); err != nil {
	// 			fmt.Println("handshake error:", err)
	// 			return
	// 		}
	// 	}()
	// }
	// wg.Wait()

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

	var response TrackerResponse
	if err := bencode.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	// return parsePeersBinary(response.Peers)
	return response.Peers, nil
}
