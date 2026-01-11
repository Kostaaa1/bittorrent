package torrent

import (
	"crypto/sha1"
	"fmt"
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
	blockCount int
	buffer     []byte
}

func (p *piece) writeBlock(begin int, block []byte) {
	copy(p.buffer[begin:], block)
	p.blockCount++
}

type PieceWorker struct {
	hashedPieces      [][20]byte
	numWorkers        int
	numBlocksPerPiece int
	blockSize         int
	pieceLength       int
	worker            chan PieceMessage
	pieces            map[int]*piece
	writer            *PieceWriter
}

func NewPieceWorker(
	workers int,
	hashedPieces [][20]byte,
	paths []File,
	pieceLength int,
) *PieceWorker {
	blockSize := int(math.Pow(2, 14))

	return &PieceWorker{
		pieceLength:       pieceLength,
		hashedPieces:      hashedPieces,
		numWorkers:        workers,
		worker:            make(chan PieceMessage),
		pieces:            make(map[int]*piece),
		blockSize:         blockSize,
		numBlocksPerPiece: pieceLength / blockSize,
		writer:            NewPieceWriter(paths),
	}
}

func (pm *PieceWorker) findOrAssignPiece(pieceIndex int) *piece {
	if block, ok := pm.pieces[pieceIndex]; ok {
		return block
	}
	pm.pieces[pieceIndex] = &piece{
		blockCount: 0,
		buffer:     make([]byte, pm.pieceLength),
	}
	return pm.pieces[pieceIndex]
}

func (pm *PieceWorker) Start() error {
	for msg := range pm.worker {
		piece := pm.findOrAssignPiece(msg.index)
		piece.writeBlock(msg.begin, msg.block)

		if piece.blockCount == pm.numBlocksPerPiece {
			if sha1.Sum(piece.buffer) != pm.hashedPieces[msg.index] {
				return fmt.Errorf("hashes do not match for piece %d", msg.index)
			}

			fmt.Printf("[VERIFIED] Writing - piece: %d, block: %d\n",
				msg.index,
				len(msg.block),
			)

			if _, err := pm.writer.WritePiece(int64(msg.index), piece.buffer); err != nil {
				fmt.Println("failed to write piece buffer to file:", err)
				return err
			}
			delete(pm.pieces, msg.index)
		}
	}

	return nil
}

type FileEntry struct {
	file        *os.File
	Path        string
	Length      int64
	StartOffset int64
	EndOffset   int64
}

// TODO: implement API for reading from disk and serving pieces and blocks
type PieceWriter struct {
	files  []*FileEntry
	currID int
}

func NewPieceWriter(files []File) *PieceWriter {
	flusher := &PieceWriter{
		files:  make([]*FileEntry, len(files)),
		currID: 0,
	}

	var start, end int64
	for i, file := range files {
		end += file.Length
		flusher.files[i] = &FileEntry{
			StartOffset: start,
			EndOffset:   end,
			Length:      file.Length,
			Path:        file.FullPath,
		}
		start += file.Length
	}

	return flusher
}

func (w *PieceWriter) getFileForWriting(pieceOffset int64) *FileEntry {
	if pieceOffset == 0 {
		return w.files[0]
	}
	for i, f := range w.files {
		if pieceOffset <= f.StartOffset {
			return w.files[i-1]
		}
	}
	return nil
}

func (w *PieceWriter) WritePiece(pieceIndex int64, piece []byte) (int, error) {
	pieceOffset := pieceIndex * 32768
	entry := w.getFileForWriting(pieceOffset)

	if entry.file == nil {
		if err := os.MkdirAll(filepath.Dir(entry.Path), 0755); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(entry.Path, os.O_CREATE|os.O_WRONLY, 0744)
		if err != nil {
			return 0, err
		}
		entry.file = f
	}

	// TODO: Need a way to check if piece belongs to MORE files (not just 2), need to split the byte by offsets and write them corraspondingly
	fmt.Println("Writing:", entry.Path, pieceIndex, pieceOffset, entry.StartOffset, entry.Length)

	if pieceOffset > entry.EndOffset {
		dif := pieceOffset - entry.EndOffset

		endPiece := piece[:dif]
		startPiece := piece[dif+1:]

		_, err := entry.file.WriteAt(endPiece, pieceOffset)
		if err != nil {
			return 0, err
		}

		// write piece for next file
		w.currID++
		return w.WritePiece(pieceIndex, startPiece)
	}

	_, err := entry.file.WriteAt(piece, pieceOffset)
	if err != nil {
		return 0, err
	}

	return 0, nil
}
