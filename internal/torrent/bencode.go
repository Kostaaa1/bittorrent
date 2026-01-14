package torrent

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"path/filepath"
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

type bencodeFile struct {
	Length int      `bencode:"length"`
	Path   []string `bencode:"path"`
}

type bencodeInfo struct {
	Name        string        `bencode:"name"`
	Length      *int          `bencode:"length,omitempty"`
	Files       []bencodeFile `bencode:"files,omitempty"`
	PieceLength int           `bencode:"piece length"`
	Pieces      string        `bencode:"pieces"`
	Private     int           `bencode:"private"`
}

type bencodePeer struct {
	PeerID string `bencode:"peer id"`
	IP     string `bencode:"ip"`
	Port   uint16 `bencode:"port"`
}

func (p *bencodePeer) ip4addr() string {
	return fmt.Sprintf("%s:%d", p.IP, p.Port)
}

func (i bencodeInfo) totalLength() (int, error) {
	if i.Length != nil {
		return *i.Length, nil
	}

	if len(i.Files) > 0 {
		length := 0
		for _, f := range i.Files {
			length += f.Length
		}
		return length, nil
	}

	return 0, errors.New("invalid length: length and files are missing in bencode info")
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

func (bto *bencodeTorrent) prepareFileEntries() []*FileEntry {
	files := make([]*FileEntry, len(bto.Info.Files))
	var start, end int

	for i, file := range bto.Info.Files {
		end += file.Length

		name := append([]string{bto.Info.Name}, file.Path...)
		fullPath := filepath.Join(name...)

		files[i] = &FileEntry{
			Length:      file.Length,
			FullPath:    fullPath,
			StartOffset: start,
			EndOffset:   end,
		}
		start += file.Length
	}

	return files
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

	total, err := bto.Info.totalLength()
	if err != nil {
		return nil, err
	}

	return &TorrentFile{
		Announce:    bto.Announce,
		Name:        bto.Info.Name,
		TotalLength: total,
		PieceLength: bto.Info.PieceLength,
		InfoHash:    hash,
		Pieces:      pieces,
		Files:       bto.prepareFileEntries(),
	}, nil
}
