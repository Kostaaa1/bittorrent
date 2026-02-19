package io

import (
	"crypto/sha1"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"test/internal/torrent/peer"
)

type FileEntry struct {
	ID          int
	file        *os.File
	FullPath    string
	Length      int
	StartOffset int
	EndOffset   int
}

type PieceBuffer struct {
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

func (pb *PieceBuffer) verify() bool {
	return sha1.Sum(pb.buffer) == pb.hash
}

func (pb *PieceBuffer) writeBlock(begin int, block []byte) {
	copy(pb.buffer[begin:], block)
	pb.blockCount++
}

type PieceWriter struct {
	numBlocksPerPiece int
	numOfPieces       int
	totalLength       int
	pieceLength       int
	blockSize         int
	hashPieces        [][20]byte
	pieces            map[int]*PieceBuffer
	worker            chan peer.PieceMessage
	files             []*FileEntry
	results           chan Result
}

func NewPieceWriter(
	pieceLength int,
	pieces [][20]byte,
	entries []*FileEntry,
	totalLength,
	numBlocksPerPiece int,
	blockSize int,
) *PieceWriter {
	return &PieceWriter{
		worker:            make(chan peer.PieceMessage),
		results:           make(chan Result),
		pieces:            make(map[int]*PieceBuffer),
		files:             entries,
		hashPieces:        pieces,
		pieceLength:       pieceLength,
		blockSize:         blockSize,
		numBlocksPerPiece: numBlocksPerPiece,
		numOfPieces:       len(pieces),
		totalLength:       totalLength,
	}
}

func (pw *PieceWriter) Channles() (chan peer.PieceMessage, <-chan Result) {
	return pw.worker, pw.results
}

func (pw *PieceWriter) getPieceSize(pieceIndex int) (int, int) {
	size := pw.pieceLength
	blocks := pw.numBlocksPerPiece
	lastPiece := pw.numOfPieces - 1
	if pieceIndex == lastPiece {
		size = pw.totalLength - (lastPiece * pw.pieceLength)
		blocks = int(math.Ceil(float64(size) / float64(pw.blockSize)))
	}
	return size, blocks
}

func (pw *PieceWriter) writeBlock(msg peer.PieceMessage) *PieceBuffer {
	pbuf := pw.pieces[msg.Index]

	if pbuf == nil {
		size, blocks := pw.getPieceSize(msg.Index)
		pbuf = &PieceBuffer{
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
			} else if _, err := pw.writePiece(msg.Index, piece.buffer); err != nil {
				result.Err = err
			}

			pw.results <- result
		}
	}

	return nil
}

func (w *PieceWriter) getFileEntry(pieceIndex int) (entry *FileEntry, id int, err error) {
	offset := pieceIndex * w.pieceLength
	for i, f := range w.files {
		if offset <= f.EndOffset {
			id = i
			entry = f
			return
		}
	}
	err = fmt.Errorf("failed to find the file for piece index: %d", pieceIndex)
	return
}

func (w *PieceWriter) setEntryFile(entry *FileEntry) error {
	if entry.file != nil {
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
	entry.file = f

	return nil
}

func (w *PieceWriter) writePiece(pieceIndex int, piece []byte) (int, error) {
	entry, fileID, err := w.getFileEntry(pieceIndex)
	if err != nil {
		return 0, err
	}

	if err := w.setEntryFile(entry); err != nil {
		return 0, err
	}

	pieceOffset := pieceIndex * w.pieceLength
	entryOffset := pieceOffset - entry.StartOffset

	if entryOffset+len(piece) > entry.Length {
		diff := entry.Length - entryOffset
		start := piece[:diff]

		remainder := piece[diff:]
		remainderLen := len(remainder)

		if _, err := entry.file.WriteAt(start, int64(entryOffset)); err != nil {
			return 0, err
		}

		for remainderLen > 0 {
			fileID++
			entry = w.files[fileID]

			if err := w.setEntryFile(entry); err != nil {
				return 0, err
			}

			remainderN, err := entry.file.WriteAt(remainder, 0)
			if err != nil {
				return 0, err
			}
			remainderLen -= remainderN
		}

		return len(piece), nil
	}

	return entry.file.WriteAt(piece, int64(entryOffset))
}
