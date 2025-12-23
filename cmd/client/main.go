package main

import (
	"crypto/rand"
	"crypto/sha1"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"test/pkg/bencode"
)

type TorrentFile struct {
	Announce    string
	Length      int
	Name        string
	PieceLength int
	Pieces      [][20]byte
	InfoHash    [20]byte
}

type bencodeTorrent struct {
	Announce string      `bencode:"announce"`
	Info     bencodeInfo `bencode:"info"`
}

type bencodeInfo struct {
	Name        string `bencode:"name"`
	Length      int    `bencode:"length"`
	PieceLength int    `bencode:"piece length"`
	Pieces      string `bencode:"pieces"`
}

func (i bencodeInfo) hash() ([20]byte, error) {
	buf, err := bencode.Marshal(i)
	if err != nil {
		return [20]byte{}, err
	}
	return sha1.Sum(buf), nil
}

func (info *bencodeInfo) readPieces() ([][20]byte, error) {
	buf := []byte(info.Pieces)

	if len(buf)%sha1.Size != 0 {
		return nil, errors.New("")
	}

	numPieces := len(info.Pieces) / sha1.Size
	pieces := make([][20]byte, numPieces)

	for i := range numPieces {
		copy(pieces[i][:], buf[i*sha1.Size:(i+1)*sha1.Size])
	}

	return pieces, nil
}

func (bto *bencodeTorrent) toTorrentFile() (*TorrentFile, error) {
	pieces, err := bto.Info.readPieces()
	if err != nil {
		return nil, err
	}

	hash, err := bto.Info.hash()
	if err != nil {
		return nil, err
	}

	return &TorrentFile{
		Announce:    bto.Announce,
		Name:        bto.Info.Name,
		Length:      bto.Info.Length,
		PieceLength: bto.Info.PieceLength,
		InfoHash:    hash,
		Pieces:      pieces,
	}, nil
}

type bencodePeer struct {
	PeerID *string `bencode:"peer id"`
	IP     string  `bencode:"ip"`
	Port   uint16  `bencode:"port"`
}

type TrackerResponse struct {
	FailureReason  string `bencode:"failure reason"`
	WarningMessage string `bencode:"warning reason"`
	Interval       int    `bencode:"interval"`
	MinInterval    int    `bencode:"min interval"`
	TrackerID      string `bencode:"tracker id"`
	Complete       int    `bencode:"complete"`
	Incomplete     int    `bencode:"incomplete"`
	// Peers          []interface{} `bencode:"peers"`
	// Peers  []map[string]interface{} `bencode:"peers"`
	Peers  []bencodePeer `bencode:"peers"`
	Peers6 string        `bencode:"peers6"`
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

func (tf *TorrentFile) discoverPeers(peerID [20]byte, port uint16) error {
	trackerURL, err := tf.buildHttpTrackerURL(peerID, port)
	if err != nil {
		return err
	}

	resp, err := http.Get(trackerURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var response TrackerResponse
	if err := bencode.NewDecoder(resp.Body).Decode(&response); err != nil {
		return err
	}

	fmt.Println(response)

	return nil
}

func getPeerID() ([20]byte, error) {
	buf := [20]byte{}

	_, err := rand.Read(buf[:])
	if err != nil {
		return buf, err
	}

	return buf, nil
}

func main() {
	f, err := os.Open("file2.torrent")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	var src bencodeTorrent
	if err := bencode.NewDecoder(f).Decode(&src); err != nil {
		log.Fatal(err)
	}

	tf, err := src.toTorrentFile()
	if err != nil {
		log.Fatal(err)
	}

	peerID, err := getPeerID()
	if err != nil {
		log.Fatal(err)
	}

	err = tf.discoverPeers(peerID, 6881)
	if err != nil {
		log.Fatal(err)
	}
}
