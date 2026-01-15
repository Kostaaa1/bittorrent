package torrent

import (
	"crypto/sha1"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type pieceState int

const (
	pieceMissing pieceState = iota
	pieceDownloading
	pieceCompleted
)

type piece struct {
	// to check if its written the number of times of blocks in single piece
	blockCount int
	buffer     []byte
}

func (p *piece) writeBlock(begin int, block []byte) {
	copy(p.buffer[begin:], block)
	p.blockCount++
}

type PieceWriter struct {
	hashedPieces      [][20]byte
	numWorkers        int
	numBlocksPerPiece int
	totalLength       int
	pieceLength       int
	blockSize         int
	worker            chan PieceMessage
	pieces            map[int]*piece
	files             []*FileEntry
}

func (pw *PieceWriter) pieceSize(pieceIndex int) int {
	lastPiece := len(pw.hashedPieces) - 1
	if pieceIndex == lastPiece {
		return pw.totalLength - (lastPiece * pw.pieceLength)
	}
	return pw.pieceLength
}

func (pw *PieceWriter) findOrAssignPiece(pieceIndex int) *piece {
	if block, ok := pw.pieces[pieceIndex]; ok {
		return block
	}
	size := pw.pieceSize(pieceIndex)

	pw.pieces[pieceIndex] = &piece{
		blockCount: 0,
		buffer:     make([]byte, size),
	}

	return pw.pieces[pieceIndex]
}

func (pw *PieceWriter) Start() error {
	for msg := range pw.worker {
		piece := pw.findOrAssignPiece(msg.index)
		piece.writeBlock(msg.begin, msg.block)

		// NOTE: revisit this and possibly find better way of veyfing if its the last block that is missing.
		if piece.blockCount == pw.numBlocksPerPiece {
			// TODO: need to notify the network layer if verification succeeds or fails. USE SEPARATE CHANNEL
			if sha1.Sum(piece.buffer) != pw.hashedPieces[msg.index] {
				return fmt.Errorf("hashes do not match for piece %d", msg.index)
			}

			fmt.Printf("[VERIFIED] Writing - piece: %d, block: %d\n", msg.index, len(msg.block))

			// NOTE: What if writing of the piece fails, how to recover?
			if _, err := pw.WritePiece(msg.index, piece.buffer); err != nil {
				fmt.Println("failed to write piece buffer to file:", err)
				return err
			}
			delete(pw.pieces, msg.index)
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

		////////////////
		// TODO: This still has bugs
		// Write as long as remainder buffer can be written to file
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
