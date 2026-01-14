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
	hashedPieces [][20]byte
	numWorkers   int

	numBlocksPerPiece int
	totalLength       int
	pieceLength       int
	blockSize         int

	worker chan PieceMessage
	pieces map[int]*piece
	files  []*FileEntry
	currID int
}

func (pm *PieceWriter) pieceSize(pieceIndex int) int {
	lastPiece := len(pm.hashedPieces) - 1
	if pieceIndex == lastPiece {
		return pm.totalLength - (lastPiece * pm.pieceLength)
	}
	return pm.pieceLength
}

func (pm *PieceWriter) findOrAssignPiece(pieceIndex int) *piece {
	if block, ok := pm.pieces[pieceIndex]; ok {
		return block
	}
	size := pm.pieceSize(pieceIndex)

	pm.pieces[pieceIndex] = &piece{
		blockCount: 0,
		buffer:     make([]byte, size),
	}

	return pm.pieces[pieceIndex]
}

func (pm *PieceWriter) Start() error {
	for msg := range pm.worker {
		piece := pm.findOrAssignPiece(msg.index)
		piece.writeBlock(msg.begin, msg.block)

		fmt.Println("received piece:", msg.index, msg.begin, len(msg.block), piece.blockCount, pm.numBlocksPerPiece)

		// NOTE: revisit this and possibly find better way of veyfing if its the last block that is missing.
		if piece.blockCount == pm.numBlocksPerPiece {
			// TODO: need to notify the network layer if verification succeeds or fails. USE SEPARATE CHANNEL
			if sha1.Sum(piece.buffer) != pm.hashedPieces[msg.index] {
				return fmt.Errorf("hashes do not match for piece %d", msg.index)
			}

			fmt.Printf("[VERIFIED] Writing - piece: %d, block: %d\n",
				msg.index,
				len(msg.block),
			)

			// NOTE: What if writing of the piece fails, how to recover?
			_, err := pm.WritePiece(msg.index, piece.buffer)
			if err != nil {
				fmt.Println("failed to write piece buffer to file:", err)
				return err
			}
			delete(pm.pieces, msg.index)
		}
	}

	return nil
}

func (w *PieceWriter) WritePiece(pieceIndex int, piece []byte) (int, error) {
	entry := w.files[w.currID]
	pieceOffset := pieceIndex*w.pieceLength - entry.StartOffset

	if entry.file == nil {
		if err := os.MkdirAll(filepath.Dir(entry.FullPath), 0755); err != nil {
			fmt.Println("Failed to os.Mkdirall", err)
		}
		f, err := os.OpenFile(entry.FullPath, os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return 0, err
		}
		entry.file = f
	}

	overlap := pieceOffset+w.pieceLength > entry.EndOffset

	if overlap {
		diff := entry.EndOffset - pieceOffset
		start := piece[:diff]
		end := piece[diff:]

		startN, err := entry.file.WriteAt(start, int64(pieceOffset))
		if err != nil {
			log.Fatal("failed to writeAt", entry.FullPath, err)
		}

		// TODO: BUG:
		// CHECK OFFSET LENGTH!
		w.currID++
		entry = w.files[w.currID]

		f, err := os.OpenFile(entry.FullPath, os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal("failed to openFile", entry.FullPath, err)
		}
		entry.file = f

		endN, err := entry.file.Write(end)
		if err != nil {
			log.Fatal("failed to writeAt", entry.FullPath, err)
		}

		return startN + endN, nil
	} else {
		n, err := entry.file.WriteAt(piece, int64(pieceOffset))
		if err != nil {
			return 0, err
		}
		return n, nil
	}
}
