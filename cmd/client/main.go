package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/jackpal/bencode-go"
)

type bencodeInfo struct {
	Length      int    `bencode:"length"`
	Name        string `bencode:"name"`
	PieceLength int    `bencode:"piece length"`
	Pieces      string `bencode:"pieces"`
}

func (info bencodeInfo) hash() ([20]byte, error) {
	buf := bytes.Buffer{}
	if err := bencode.Marshal(&buf, info); err != nil {
		return [20]byte{}, err
	}
	return sha1.Sum(buf.Bytes()), nil
}

func (info bencodeInfo) readHashPieces() ([][20]byte, error) {
	if len(info.Pieces)%sha1.Size != 0 {
		return nil, errors.New("malformed torrent file: length of pieces is not divisible by 20 (sha1 hash size)")
	}

	numHashes := len(info.Pieces) / sha1.Size
	pieces := make([][20]byte, numHashes)
	buf := []byte(info.Pieces)

	for i := range numHashes {
		copy(pieces[i][:], buf[i*sha1.Size:(i+1)*sha1.Size])
	}

	return pieces, nil
}

type bencodeTorrentFile struct {
	Announce string      `bencode:"announce"`
	Info     bencodeInfo `bencode:"info"`
}

func (bto *bencodeTorrentFile) toTorrentFile() (*TorrentFile, error) {
	hash, err := bto.Info.hash()
	if err != nil {
		return nil, err
	}

	pieces, err := bto.Info.readHashPieces()
	if err != nil {
		return nil, err
	}

	tf := &TorrentFile{
		Announce:    bto.Announce,
		Name:        bto.Info.Name,
		Length:      bto.Info.Length,
		PieceLength: bto.Info.PieceLength,
		InfoHash:    hash,
		Pieces:      pieces,
	}

	return tf, nil
}

type TorrentFile struct {
	Announce    string
	Name        string
	Length      int
	PieceLength int
	Pieces      [][20]byte
	InfoHash    [20]byte
}

func (tf *TorrentFile) buildHTTPTrackerURL(peerID [20]byte, port uint16) (string, error) {
	u, err := url.Parse(tf.Announce)
	if err != nil {
		return "", err
	}

	// some trackers accepts only compact=1 for saving bandswidth
	vals := url.Values{
		"info_hash":  []string{string(tf.InfoHash[:])},
		"peer_id":    []string{string(peerID[:])},
		"port":       []string{strconv.Itoa(int(port))},
		"left":       []string{strconv.Itoa(tf.PieceLength)},
		"downloaded": []string{"0"},
		"uploaded":   []string{"0"},
		"compact":    []string{"1"},
	}

	u.RawQuery = vals.Encode()

	return u.String(), nil
}

// Handle peers dictionary/binary format
type bencodeTrackerResponse struct {
	Interval       int    `bencode:"interval"`
	MinInterval    int    `bencode:"min interval"`
	FailureReason  string `bencode:"failure reason"`
	WarningMessage string `bencode:"warning message"`
	TrackerID      string `bencode:"tracker id"`
	Complete       int    `bencode:"complete"`
	Incomplete     int    `bencode:"incomplete"`
	Peers          string `bencode:"peers"`
}

type peersDictionary struct {
	PeerID string `bencode:"peer id"`
	IP     string `bencode:"ip"`
	Port   int    `bencode:"port"`
}

func readBinaryPeers(peers string) ([]string, error) {
	if len(peers)%6 != 0 {
		return nil, errors.New("failed to read peers: invalid peers format (not divisible by 6)")
	}

	numPeers := len(peers) / 6
	data := make([]string, numPeers)

	for i := range numPeers {
		data[i] = fmt.Sprintf("%d.%d.%d.%d:%d",
			peers[i+0],
			peers[i+1],
			peers[i+2],
			peers[i+3],
			binary.BigEndian.Uint16([]byte{peers[i+4], peers[i+5]}),
		)
	}

	return data, nil
}

func (tf *TorrentFile) discoverPeers(peerID [20]byte, port uint16) ([]string, error) {
	u, err := tf.buildHTTPTrackerURL(peerID, port)
	if err != nil {
		return nil, err
	}

	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var response bencodeTrackerResponse
	if err := bencode.Unmarshal(resp.Body, &response); err != nil {
		return nil, err
	}

	fmt.Println(response)

	if response.FailureReason != "" {
		return nil, fmt.Errorf("failed to discover peers: %v", response)
	}

	peers, err := readBinaryPeers(response.Peers)
	if err != nil {
		return nil, err
	}

	return peers, nil
}

func clientID() ([20]byte, error) {
	buf := [20]byte{}
	_, err := rand.Read(buf[:])
	if err != nil {
		return [20]byte{}, err
	}
	return buf, nil
}

type Handshake struct {
	Pstrlen  uint8
	Pstr     string
	Reserved [8]byte
	InfoHash [20]byte
	PeerID   [20]byte
}

func (h *Handshake) Bytes() []byte {
	b := make([]byte, 49+h.Pstrlen)
	b = append(b, h.Pstrlen)
	b = append(b, []byte(h.Pstr)...)
	b = append(b, h.Reserved[:]...)
	b = append(b, h.InfoHash[:]...)
	b = append(b, h.PeerID[:]...)
	return b
}

func main() {
	f, err := os.Open("file.torrent")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	var bto bencodeTorrentFile
	if err := bencode.Unmarshal(f, &bto); err != nil {
		panic(err)
	}

	tf, err := bto.toTorrentFile()
	if err != nil {
		panic(err)
	}

	port := 6881
	id, err := clientID()
	if err != nil {
		panic(err)
	}

	peers, err := tf.discoverPeers(id, uint16(port))
	if err != nil {
		panic(err)
	}

	handshake := Handshake{
		Pstrlen:  19,
		Pstr:     "BitTorrent Protocol",
		Reserved: [8]byte{},
		InfoHash: tf.InfoHash,
		PeerID:   id,
	}

	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			conn, err := net.Dial("tcp", peer)
			if err != nil {
				fmt.Println("dial failed:", err)
				return
			}

			conn.SetReadDeadline(time.Now().Add(time.Second * 5))

			if _, err := conn.Write(handshake.Bytes()); err != nil {
				panic(err)
			}

			for {
				buf := make([]byte, 4096)

				n, err := conn.Read(buf)
				if err != nil {
					if err == io.EOF {
						fmt.Println("Handle io.EOF")
						break
					}
					fmt.Println("ERROR:", err)
					return
				}

				fmt.Println(string(buf[:n]))
			}
		}()
	}

	wg.Wait()
}
