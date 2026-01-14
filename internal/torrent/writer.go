package torrent

import (
	"crypto/sha1"
	"fmt"
	"log"
	"math"
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

type FileEntry struct {
	file        *os.File
	Path        string
	Length      int
	StartOffset int
	EndOffset   int
}

type PieceWriter struct {
	hashedPieces      [][20]byte
	numWorkers        int
	numBlocksPerPiece int
	numOfPieces       int
	totalLength       int
	blockSize         int
	pieceLength       int
	worker            chan PieceMessage
	pieces            map[int]*piece
	files             []*FileEntry
	currID            int
}

func NewPieceWriter(
	workers int,
	hashedPieces [][20]byte,
	paths []File,
	totalLength int,
	pieceLength int,
	numOfPieces int,
) *PieceWriter {
	blockSize := int(math.Pow(2, 14))

	files := make([]*FileEntry, len(paths))

	var start, end int
	for i, path := range paths {
		end += path.Length
		files[i] = &FileEntry{
			Length:      path.Length,
			Path:        path.FullPath,
			StartOffset: start,
			EndOffset:   end,
		}
		start += path.Length
	}

	return &PieceWriter{
		numOfPieces:       numOfPieces,
		totalLength:       totalLength,
		pieceLength:       pieceLength,
		hashedPieces:      hashedPieces,
		numWorkers:        workers,
		worker:            make(chan PieceMessage),
		pieces:            make(map[int]*piece),
		blockSize:         blockSize,
		numBlocksPerPiece: pieceLength / blockSize,
		files:             files,
		currID:            0,
	}
}

func (pm *PieceWriter) pieceSize(pieceIndex int) int {
	// need to calculate the size for last piece
	if pieceIndex == pm.numOfPieces-1 {
		return pm.totalLength - ((pm.numOfPieces - 1) * pm.pieceLength)
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

			// What if writing of the piece fails, how to recover?
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
		if err := os.MkdirAll(filepath.Dir(entry.Path), 0755); err != nil {
			fmt.Println("Failed to os.Mkdirall", err)
		}
		f, err := os.OpenFile(entry.Path, os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return 0, err
		}
		entry.file = f
	}

	fmt.Println("WRITING PIECE:", entry.Path, pieceOffset, len(piece))

	// check if next piece is overlapped, belongs to multiple files
	if pieceOffset+w.pieceLength > entry.EndOffset {
		fmt.Println("OVEFLOW")
		diff := entry.EndOffset - pieceOffset
		start := piece[:diff]
		end := piece[diff:]

		startN, err := entry.file.WriteAt(start, int64(pieceOffset))
		if err != nil {
			log.Fatal("failed to writeAt", entry.Path, err)
		}

		// TODO: BUG:
		// CHECK OFFSET LENGTH!
		// WRITE OFFSET TO DIFFERENT FILES UNTIL THE END
		w.currID++
		entry = w.files[w.currID]

		f, err := os.OpenFile(entry.Path, os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal("failed to openFile", entry.Path, err)
		}
		entry.file = f

		endN, err := entry.file.Write(end)
		if err != nil {
			log.Fatal("failed to writeAt", entry.Path, err)
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
