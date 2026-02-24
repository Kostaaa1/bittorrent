package io

import (
	"crypto/sha1"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"test/internal/torrent"
	"test/internal/torrent/peer"
)

type pieceBuffer struct {
	index       int
	size        int
	hash        [20]byte
	buffer      []byte
	blockCount  int
	totalBlocks int
}

type Result struct {
	Index    int
	Begin    int
	LenBlock int
	Err      error
}

func (pb *pieceBuffer) verify() bool {
	return sha1.Sum(pb.buffer) == pb.hash
}

func (pb *pieceBuffer) writeBlock(begin int, block []byte) {
	copy(pb.buffer[begin:], block)
	pb.blockCount++
}

type PieceWriter struct {
	info       *torrent.TorrentInfo
	pieces     map[int]*pieceBuffer
	worker     chan peer.PieceMessage
	files      []*torrent.FileEntry
	results    chan Result
	log        *log.Logger
	hashPieces [][20]byte
}

func NewPieceWriter(
	info *torrent.TorrentInfo,
	pieces [][20]byte,
	entries []*torrent.FileEntry,
) *PieceWriter {
	f, _ := os.OpenFile("log.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	log := log.New(f, "", log.Ltime)
	return &PieceWriter{
		hashPieces: pieces,
		worker:     make(chan peer.PieceMessage),
		results:    make(chan Result),
		pieces:     make(map[int]*pieceBuffer),
		files:      entries,
		info:       info,
		log:        log,
	}
}

func (pw *PieceWriter) Channles() (chan peer.PieceMessage, <-chan Result) {
	return pw.worker, pw.results
}

func (pw *PieceWriter) getPieceSize(pieceID int) (int, int) {
	size := pw.info.PieceLength
	blocks := pw.info.NumBlocksPerPiece
	lastPiece := pw.info.NumOfPieces - 1
	if pieceID == lastPiece {
		size = pw.info.TotalLength - (lastPiece * pw.info.PieceLength)
		blocks = int(math.Ceil(float64(size) / float64(pw.info.BlockSize)))
	}
	return size, blocks
}

func (pw *PieceWriter) writeBlock(msg peer.PieceMessage) *pieceBuffer {
	pbuf := pw.pieces[msg.Index]
	if pbuf == nil {
		size, blocks := pw.getPieceSize(msg.Index)
		pbuf = &pieceBuffer{
			totalBlocks: blocks,
			index:       msg.Index,
			blockCount:  0,
			size:        size,
			buffer:      make([]byte, size),
			hash:        pw.hashPieces[msg.Index],
		}
	}
	pbuf.writeBlock(msg.Begin, msg.Block)
	pw.pieces[msg.Index] = pbuf
	return pbuf
}

func (pw *PieceWriter) Start() error {
	for msg := range pw.worker {
		piece := pw.writeBlock(msg)

		if piece.blockCount == piece.totalBlocks {
			result := Result{
				Index:    msg.Index,
				Begin:    msg.Begin,
				LenBlock: len(msg.Block),
			}
			if !piece.verify() {
				result.Err = fmt.Errorf("hashes do not match for piece %d", msg.Index)
				fmt.Println("TARGET:", msg.Index, piece.index)
				fmt.Println("Block:", sha1.Sum(msg.Block))
				fmt.Println("Buffer:", sha1.Sum(piece.buffer))
				fmt.Println("Match hash piece:", piece.hash)
				// for _, hash := range pw.hashPieces {
				// 	fmt.Println(hash)
				// }
			} else if _, err := pw.writePiece(msg.Index, piece.buffer); err != nil {
				result.Err = err
			}

			delete(pw.pieces, piece.index)
			pw.results <- result
		}
	}

	return nil
}

func (w *PieceWriter) getFileEntry(pieceID int) (entry *torrent.FileEntry, id int, err error) {
	offset := pieceID * w.info.PieceLength
	for i, f := range w.files {
		if offset <= f.EndOffset {
			id = i
			entry = f
			return
		}
	}
	err = fmt.Errorf("failed to find the file for piece index: %d", pieceID)
	return
}

func (w *PieceWriter) setEntryFile(entry *torrent.FileEntry) error {
	if entry.File != nil {
		return nil
	}

	fullPath := entry.FullPath
	dir := filepath.Dir(fullPath)

	info, err := os.Stat(dir)
	if err == nil && !info.IsDir() {
		return fmt.Errorf("%s exists but is not a directory", dir)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	entry.File = f

	return nil
}

func (w *PieceWriter) writePiece(pieceID int, piece []byte) (int, error) {
	entry, fileID, err := w.getFileEntry(pieceID)
	if err != nil {
		return 0, err
	}

	if err := w.setEntryFile(entry); err != nil {
		return 0, err
	}

	pieceOffset := pieceID * w.info.PieceLength
	entryOffset := pieceOffset - entry.StartOffset

	if entryOffset+len(piece) > entry.Length {
		diff := entry.Length - entryOffset
		start := piece[:diff]
		remainder := piece[diff:]
		remainderLen := len(remainder)

		w.log.Println("[DOWNLOADED PIECE]", "piece_index=", pieceID, len(piece))

		if _, err := entry.File.WriteAt(start, int64(entryOffset)); err != nil {
			return 0, err
		}

		for remainderLen > 0 {
			fileID++
			entry = w.files[fileID]

			if err := w.setEntryFile(entry); err != nil {
				return 0, err
			}

			remainderN, err := entry.File.WriteAt(remainder, 0)
			if err != nil {
				return 0, err
			}
			remainderLen -= remainderN
		}

		return len(piece), nil
	}

	w.log.Println("[DOWNLOADED PIECE]", "piece_index=", pieceID, len(piece))

	return entry.File.WriteAt(piece, int64(entryOffset))
}
