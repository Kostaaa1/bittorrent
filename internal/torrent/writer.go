package torrent

import (
	"crypto/sha1"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

type pieceState int

const (
	PieceMissing pieceState = iota
	PieceAssigned
	PieceInFlight
	PieceDownloaded
)

type Piece struct {
	index int
	state pieceState
	size  int
	hash  [20]byte
	// NOTE: revisit, blockCount might be bad
	blockCount int8
	buffer     []byte
}

func NewPiece(index int, hash [20]byte, length int) *Piece {
	return &Piece{
		index: index,
		hash:  hash,
		size:  length,
	}
}

func (p *Piece) write(begin int, block []byte) {
	copy(p.buffer[begin:], block)
	p.blockCount++
}
func (p *Piece) verify() bool { return sha1.Sum(p.buffer) == p.hash }
func (p *Piece) alloc()       { p.buffer = make([]byte, p.size) }

type PieceWriter struct {
	numWorkers        int
	numBlocksPerPiece int8
	pieceLength       int
	worker            chan PieceMessage
	pieces            map[int]*Piece
	files             []*FileEntry
	results           chan<- Result
}

func (pw *PieceWriter) writeBlock(msg PieceMessage) *Piece {
	piece := pw.pieces[msg.index]
	if piece.buffer == nil {
		piece.alloc()
	}
	piece.write(msg.begin, msg.block)
	return piece
}

func (pw *PieceWriter) Start() error {
	var wg sync.WaitGroup

	for i := 0; i < pw.numWorkers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for msg := range pw.worker {
				piece := pw.writeBlock(msg)

				if piece.blockCount == pw.numBlocksPerPiece {
					result := Result{
						Index: msg.index,
						Begin: msg.begin,
					}

					if !piece.verify() {
						result.Err = fmt.Errorf("hashes do not match for piece %d", msg.index)
						pw.results <- result
					} else if _, err := pw.WritePiece(msg.index, piece.buffer); err != nil {
						result.Err = err
					}

					pw.results <- result
				}
			}
		}()
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
	fmt.Printf("[VERIFIED] writing piece: index=%d\n", pieceIndex)

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
