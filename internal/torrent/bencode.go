package torrent

import (
	"crypto/sha1"
	"errors"
	"path/filepath"
	"test/internal/torrent/io"
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
	Comment      string      `bencode:"comment"`
	CreatedBy    string      `bencode:"created by"`
	CreationDate int64       `bencode:"creation date"`
	Encoding     string      `bencode:"encoding"`
	Info         bencodeInfo `bencode:"info"`
	Publisher    string      `bencode:"publisher"`
	PublisherURL string      `bencode:"publisher-url"`
	UrlList      []string    `bencode:"url-list"`
}

type bencodeFile struct {
	Length int      `bencode:"length"`
	Path   []string `bencode:"path"`
}

type bencodeInfo struct {
	Files       []bencodeFile `bencode:"files,omitempty"`
	Length      *int          `bencode:"length,omitempty"`
	Name        string        `bencode:"name"`
	PieceLength int           `bencode:"piece length"`
	Pieces      string        `bencode:"pieces"`
	Private     int           `bencode:"private"`
	Hash        [20]byte
}

func (i *bencodeInfo) RawMessage(b []byte) {
	i.Hash = sha1.Sum(b)
}

type bencodePeer struct {
	PeerID string `bencode:"peer id"`
	IP     string `bencode:"ip"`
	Port   uint16 `bencode:"port"`
}

type Peers struct {
	Compact []byte
	List    []bencodePeer
}

func (p *Peers) UnmarshalBencode(d *bencode.Decoder) error {
	switch d.PeekByte() {
	case bencode.KindList:
		if err := d.Decode(&p.List); err != nil {
			return err
		}
	default:
		if err := d.Decode(&p.Compact); err != nil {
			return err
		}
	}
	return nil
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

func (bto *bencodeTorrent) prepareFileEntries() []*io.FileEntry {
	if len(bto.Info.Files) == 0 {
		files := make([]*io.FileEntry, 1)

		l := *bto.Info.Length

		files[0] = &io.FileEntry{
			ID:          0,
			FullPath:    bto.Info.Name,
			Length:      l,
			StartOffset: 0,
			EndOffset:   l,
		}

		return files
	}

	files := make([]*io.FileEntry, len(bto.Info.Files))
	var start, end int

	for i, file := range bto.Info.Files {
		end += file.Length

		name := append([]string{bto.Info.Name}, file.Path...)
		fullPath := filepath.Join(name...)

		files[i] = &io.FileEntry{
			ID:          i,
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

	total, err := bto.Info.totalLength()
	if err != nil {
		return nil, err
	}

	entries := bto.prepareFileEntries()

	return &TorrentFile{
		Announce:     bto.Announce,
		Name:         bto.Info.Name,
		TotalLength:  total,
		PieceLength:  bto.Info.PieceLength,
		InfoHash:     bto.Info.Hash,
		Pieces:       pieces,
		Private:      bto.Info.Private,
		AnnounceList: bto.AnnounceList,
		Comment:      bto.Comment,
		CreatedBy:    bto.CreatedBy,
		CreationDate: bto.CreationDate,
		Encoding:     bto.Encoding,
		Publisher:    bto.Publisher,
		PublisherURL: bto.PublisherURL,
		Files:        entries,
		UrlList:      bto.UrlList,
	}, nil
}
