package torrent

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"test/pkg/bencode"
	"time"
)

type TorrentFile struct {
	Announce    string
	Length      int
	Name        string
	PieceLength int
	Pieces      [][20]byte
	InfoHash    [20]byte
}

type Downloader interface {
	Download(clientID [20]byte, port uint16) error
}

func NewFile(filename string) (Downloader, error) {
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
	peers, err := tf.discoverPeers(clientID, port)
	if err != nil {
		return err
	}

	hs := &Handshake{
		Pstr:      []byte("BitTorrent protocol"),
		Reserverd: [8]byte{},
		InfoHash:  tf.InfoHash,
		PeerID:    clientID,
	}

	var wg sync.WaitGroup

	for _, peer := range peers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			addr := fmt.Sprintf("%s:%d", peer.IP, peer.Port)
			fmt.Println("Dialing peer:", addr)

			conn, err := net.DialTimeout("tcp", addr, time.Second*5)
			if err != nil {
				fmt.Println("Dialing peer failed:", err)
				return
			}

			_, err = conn.Write(hs.Bytes())
			if err != nil {
				fmt.Println("failed to write handshake:", err)
				return
			}

			peerHandshake := new(Handshake)
			err = peerHandshake.Read(conn)
			if err != nil {
				fmt.Println("HANDSHAKE READING FAILED", err)
				return
			}

			fmt.Println("--- HANSHAKE SUCCESSFUL ---")

			if !compare(peerHandshake.InfoHash[:], hs.InfoHash[:]) {
				log.Println("info hashes are not matching for peer:", addr)
				return
			}

			// interest
			msg := Message{ID: 2}
			_, err = conn.Write(msg.Serialize())
			if err != nil {
				fmt.Println("failed to write the message:", err)
				return
			}

			// should be unchoke
			newMessage, err := ReadMessage(conn)
			if err != nil {
				fmt.Println("failed to read the message:", err)
				return
			}
			// fmt.Println("INTEREST RESPONSE (should be unchoke):", newMessage.ID)

			if newMessage.ID == '1' {
			}
		}()
	}

	wg.Wait()

	return nil
}

func compare(b1, b2 []byte) bool {
	if len(b1) != len(b2) {
		return false
	}
	for i := 0; i < len(b1); i++ {
		if b1[i] != b2[i] {
			return false
		}
	}
	return true
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
		"left":       []string{strconv.Itoa(tf.Length)},
		// "compact":    []string{"1"},
	}
	parsed.RawQuery = v.Encode()

	return parsed, nil
}

func toPeer(b [6]byte) Peer {
	return Peer{
		PeerID: nil,
		IP:     fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3]),
		Port:   binary.BigEndian.Uint16([]byte{b[4], b[5]}),
	}
}

func parsePeersBinary(peers []byte) ([]Peer, error) {
	if len(peers)%6 != 0 {
		return nil, fmt.Errorf("peers received in wrong format: not divisible by 6 - %d", len(peers))
	}

	numPeers := len(peers) / 6
	parsed := make([]Peer, numPeers)

	for i := range parsed {
		v := [6]byte{}
		copy(v[:], peers[i:i+6])
		parsed[i] = toPeer(v)
	}

	return parsed, nil
}

func (tf *TorrentFile) discoverPeers(peerID [20]byte, port uint16) ([]Peer, error) {
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

	fmt.Println("RESPONSE", response)

	// return parsePeersBinary(response.Peers)
	return response.Peers, nil
}
