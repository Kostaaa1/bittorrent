package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
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
	Peers          []byte `bencode:"peers"`
	Peers6         string `bencode:"peers6"`
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
		"compact":    []string{"1"},
	}

	parsed.RawQuery = v.Encode()

	return parsed, nil
}

func (tf *TorrentFile) discoverPeers(peerID [20]byte, port uint16) ([]string, error) {
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

	peers := make([]string, len(response.Peers)/6)

	r := bytes.NewReader(response.Peers)
	for {
		p := make([]byte, 6)

		n, err := r.Read(p)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		if n != 6 {
			return nil, errors.New("malformed peers data")
		}

		port := binary.BigEndian.Uint16(p[4:6])
		addr := fmt.Sprintf("%d.%d.%d.%d:%d", p[0], p[1], p[2], p[3], port)
		peers = append(peers, addr)
	}

	return peers, nil
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

	hs := Handshake{
		Pstrlen:   19,
		Pstr:      "BitTorrent protocol",
		Reserverd: [8]byte{},
		InfoHash:  tf.InfoHash,
		PeerID:    peerID,
	}

	handshake := hs.Bytes()
	fmt.Println("Handshake bytes:", handshake)

	peers, err := tf.discoverPeers(peerID, 6881)
	if err != nil {
		log.Fatal(err)
	}

	for _, peer := range peers {
		fmt.Println(peer)

		conn, err := net.DialTimeout("tcp", peer, time.Second*5)
		if err != nil {
			log.Fatal(err)
		}

		_, err = conn.Write(handshake)
		if err != nil {
			fmt.Println("failed to write handshake")
			continue
		}

	}
}

type Handshake struct {
	Pstrlen   int      `bencode:"pstrlen"`
	Pstr      string   `bencode:"pstrlen"`
	Reserverd [8]byte  `bencode:"reserved"`
	InfoHash  [20]byte `bencode:"info_hash"`
	PeerID    [20]byte `bencode:"peer_id"`
}

func (hs *Handshake) Bytes() []byte {
	buf := make([]byte, 48+len(hs.Pstr))
	buf[0] = byte(len(hs.Pstr))
	curr := 1
	curr += copy(buf[curr:], []byte(hs.Pstr))
	curr += copy(buf[curr:], hs.Reserverd[:])
	curr += copy(buf[curr:], hs.InfoHash[:])
	curr += copy(buf[curr:], hs.PeerID[:])
	return buf
}
