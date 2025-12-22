package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/jackpal/bencode-go"
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
	buf := bytes.Buffer{}
	if err := bencode.Marshal(&buf, i); err != nil {
		return [20]byte{}, err
	}
	return sha1.Sum(buf.Bytes()), nil
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

type peersDict struct {
	PeerID string `bencode:"peer id"`
	IP     string `bencode:"ip"`
	Port   uint16 `bencode:"port"`
}

type TrackerResponse struct {
	FailureReason  string `bencode:"failure reason"`
	WarningMessage string `bencode:"warning reason"`
	Interval       int    `bencode:"interval"`
	MinInterval    int    `bencode:"min interval"`
	TrackerID      int    `bencode:"tracker id"`
	Complete       int    `bencode:"complete"`
	Incomplete     int    `bencode:"complete"`
	Peers          string `bencode:"peers"`
}

func (tf *TorrentFile) buildHttpTrackerURL(peerID [20]byte, port uint16) (string, error) {
	parsed, err := url.Parse(tf.Announce)
	if err != nil {
		return "", err
	}
	v := url.Values{
		"info_hash":  []string{string(tf.InfoHash[:])},
		"peer_id":    []string{string(peerID[:])},
		"port":       []string{strconv.Itoa(int(port))},
		"uploaded":   []string{"0"},
		"downloaded": []string{"0"},
		"left":       []string{strconv.Itoa(tf.Length)},
		// "compact":    []string{"0"},
	}
	parsed.RawQuery = v.Encode()

	return parsed.String(), nil
}

func (tf *TorrentFile) discoverPeers(peerID [20]byte, port uint16) error {
	trackerURL, err := tf.buildHttpTrackerURL(peerID, port)
	if err != nil {
		return err
	}

	resp, err := http.Get(trackerURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	fmt.Println(string(b))

	// response, err := bencode.Decode(resp.Body)
	// if err != nil {
	// 	return err
	// }
	// fmt.Println("RESPONSE", response)

	// var response TrackerResponse
	// if err := bencode.Unmarshal(resp.Body, &response); err != nil {
	// 	return err
	// }

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
	f, err := os.Open("file.torrent")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	var src bencodeTorrent
	if err := bencode.Unmarshal(f, &src); err != nil {
		log.Fatal(err)
	}
	// if err := bencode.NewDecoder(f).Decode(&src); err != nil {
	// 	log.Fatal(err)
	// }

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
