package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"

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

func (tf *TorrentFile) discoverPeers(peerID [20]byte, port uint16) error {
	u, err := tf.buildHTTPTrackerURL(peerID, port)
	if err != nil {
		return err
	}

	resp, err := http.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var response bencodeTrackerResponse
	if err := bencode.Unmarshal(resp.Body, &response); err != nil {
		return err
	}

	fmt.Println(response)

	if response.FailureReason != "" {
		return fmt.Errorf("failed to discover peers: %v", response)
	}

	fmt.Println(len(response.Peers))

	return nil
}

func clientID() ([20]byte, error) {
	buf := [20]byte{}
	_, err := rand.Read(buf[:])
	if err != nil {
		return [20]byte{}, err
	}
	return buf, nil
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

	tf.discoverPeers(id, uint16(port))
}
