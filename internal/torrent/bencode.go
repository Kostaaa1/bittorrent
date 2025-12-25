package torrent

import (
	"crypto/sha1"
	"errors"
	"test/pkg/bencode"
)

type infoMode int

const (
	multifile infoMode = iota
	singlefile
)

type bencodeTorrent struct {
	Announce     string      `bencode:"announce"`
	AnnounceList [][]string  `bencode:"announce-list"`
	CreationDate int64       `bencode:"creation date"`
	Comment      string      `bencode:"comment"`
	CreatedBy    string      `bencode:"created by"`
	Encoding     string      `bencode:"encoding"`
	Info         bencodeInfo `bencode:"info"`
}

type file struct {
	Length int      `bencode:"length"`
	Path   []string `bencode:"path"`
}

type bencodeInfo struct {
	Name   string `bencode:"name"`
	Length int    `bencode:"length"`
	// Files       *[]file `bencode:"files"`
	PieceLength int    `bencode:"piece length"`
	Pieces      string `bencode:"pieces"`
	// Private     int    `bencode:"private"`
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
