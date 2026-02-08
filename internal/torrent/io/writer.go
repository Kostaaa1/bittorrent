package io

import (
	"crypto/sha1"
	"fmt"
	"log"
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
	index      int
	size       int
	hash       [20]byte
	buffer     []byte
	blockCount int8
}

type Result struct {
	Index int
	Begin int
	Err   error
}

func (pb *PieceBuffer) verify() bool {
	return sha1.Sum(pb.buffer) == pb.hash
}

func (pb *PieceBuffer) writeBlock(begin int, block []byte) {
	copy(pb.buffer[begin:], block)
	pb.blockCount++
}

type PieceWriter struct {
	numBlocksPerPiece int8
	numOfPieces       int
	totalLength       int
	pieceLength       int

	hashPieces [][20]byte
	pieces     map[int]*PieceBuffer
	worker     chan peer.PieceMessage
	files      []*FileEntry
	results    chan Result
}

func NewPieceWriter(
	pieceLength int,
	pieces [][20]byte,
	entries []*FileEntry,
	totalLength int,
	numBlocksPerPiece int8,
) *PieceWriter {
	return &PieceWriter{
		worker:            make(chan peer.PieceMessage),
		results:           make(chan Result),
		pieces:            make(map[int]*PieceBuffer),
		files:             entries,
		hashPieces:        pieces,
		pieceLength:       pieceLength,
		numBlocksPerPiece: numBlocksPerPiece,
		numOfPieces:       len(pieces),
		totalLength:       totalLength,
	}
}

func (pw *PieceWriter) Channles() (chan peer.PieceMessage, <-chan Result) {
	return pw.worker, pw.results
}

func (pw *PieceWriter) getPieceSize(pieceIndex int) int {
	size := pw.pieceLength
	lastPiece := pw.numOfPieces - 1
	if pieceIndex == lastPiece {
		size = pw.totalLength - (lastPiece * pw.pieceLength)
	}
	return size
}

func (pw *PieceWriter) writeBlock(msg peer.PieceMessage) *PieceBuffer {
	pbuf := pw.pieces[msg.Index]

	if pbuf == nil {
		size := pw.getPieceSize(msg.Index)
		pbuf = &PieceBuffer{
			index:      msg.Index,
			blockCount: 0,
			size:       size,
			buffer:     make([]byte, size),
			hash:       pw.hashPieces[msg.Index],
		}
	}

	pbuf.writeBlock(msg.Begin, msg.Block)
	pw.pieces[msg.Index] = pbuf

	return pbuf
}

func (pw *PieceWriter) Start() error {
	for msg := range pw.worker {
		piece := pw.writeBlock(msg)

		if piece.blockCount == pw.numBlocksPerPiece {
			result := Result{
				Index: msg.Index,
				Begin: msg.Begin,
			}

			if !piece.verify() {
				result.Err = fmt.Errorf("hashes do not match for piece %d", msg.Index)
				pw.results <- result
			} else if _, err := pw.WritePiece(msg.Index, piece.buffer); err != nil {
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

func (w *PieceWriter) WritePiece(pieceIndex int, piece []byte) (int, error) {
	entry, fileID, err := w.getFileEntry(pieceIndex)
	if err != nil {
		return 0, err
	}

	if entry.file == nil {
		if err := os.MkdirAll(filepath.Dir(entry.FullPath), 0755); err != nil {
			return 0, fmt.Errorf("Failed to os.Mkdirall: %v", err)
		}
		f, err := os.OpenFile(entry.FullPath, os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return 0, err
		}
		entry.file = f
	}

	pieceOffset := pieceIndex * w.pieceLength
	entryOffset := pieceOffset - entry.StartOffset

	if entryOffset+len(piece) > entry.Length {
		diff := entry.Length - entryOffset
		start := piece[:diff]

		remainder := piece[diff:]
		remainderLen := len(remainder)

		_, err := entry.file.WriteAt(start, int64(entryOffset))
		if err != nil {
			log.Fatal("failed to writeAt", entry.FullPath, err)
		}

		for remainderLen > 0 {
			fileID++
			entry = w.files[fileID]

			f, err := os.OpenFile(entry.FullPath, os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				log.Fatal("failed to openFile", entry.FullPath, err)
			}
			entry.file = f

			remainderN, err := entry.file.WriteAt(remainder, 0)
			if err != nil {
				log.Fatal("failed to writeAt", entry.FullPath, err)
			}
			remainderLen -= remainderN
		}

		return len(piece), nil
	}

	n, err := entry.file.WriteAt(piece, int64(entryOffset))
	if err != nil {
		return 0, err
	}

	return n, nil
}
