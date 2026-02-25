package peer

import (
	"crypto/sha1"
	"fmt"
	"log"
	"math"
	"os"
	"test/internal/torrent"
)

type Result struct {
	Index    int
	Begin    int
	LenBlock int
	Err      error
}

type pieceBuffer struct {
	index  int
	size   int
	hash   [20]byte
	buffer []byte
	// blockOffsets []int
	totalBlocks int
	blockCount  int
}

func (pb *pieceBuffer) verify() bool {
	return sha1.Sum(pb.buffer) == pb.hash
}

func (pb *pieceBuffer) writeBlock(begin int, block []byte) {
	// if len(pb.blockOffsets) < pb.size {
	copy(pb.buffer[begin:], block)
	pb.blockCount++
	// pb.blockOffsets = append(pb.blockOffsets, begin)
	// }
}

// func (pb *pieceBuffer) alloc() {
// 	pb.blockOffsets = make([]int, 0, pb.totalBlocks)
// 	pb.buffer = make([]byte, pb.size)
// }

type PieceWriter struct {
	info       *torrent.TorrentInfo
	pieces     map[int]*pieceBuffer
	files      []*torrent.FileEntry
	worker     chan PieceMessage
	results    chan Result
	log        *log.Logger
	hashPieces [][20]byte
}

func NewPieceWriter(
	info *torrent.TorrentInfo,
	pieces [][20]byte,
	entries []*torrent.FileEntry,
) *PieceWriter {
	f, _ := os.OpenFile("writes.log", os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)
	log := log.New(f, "", log.Ltime)
	return &PieceWriter{
		hashPieces: pieces,
		worker:     make(chan PieceMessage),
		results:    make(chan Result),
		pieces:     make(map[int]*pieceBuffer),
		files:      entries,
		info:       info,
		log:        log,
	}
}

func (pw *PieceWriter) Channels() (chan PieceMessage, chan Result) {
	return pw.worker, pw.results
}

func (pw *PieceWriter) newPieceBuffer(pieceID int) *pieceBuffer {
	size := pw.info.PieceLength
	blocks := pw.info.NumBlocksPerPiece
	lastPiece := pw.info.NumOfPieces - 1

	if pieceID == lastPiece {
		size = pw.info.TotalLength - (lastPiece * pw.info.PieceLength)
		blocks = int(math.Ceil(float64(size) / float64(pw.info.BlockSize)))
	}

	return &pieceBuffer{
		index:       pieceID,
		hash:        pw.hashPieces[pieceID],
		totalBlocks: blocks,
		// blockOffsets: make([]int, blocks),
		buffer: make([]byte, size),
		size:   size,
	}
}

func (pw *PieceWriter) writeBlock(msg PieceMessage) *pieceBuffer {
	pbuf := pw.pieces[msg.Index]
	if pbuf == nil {
		pbuf = pw.newPieceBuffer(msg.Index)
	}
	pbuf.writeBlock(msg.Begin, msg.Block)
	pw.pieces[msg.Index] = pbuf
	return pbuf
}

func (pw *PieceWriter) Run() error {
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

func (w *PieceWriter) entryForPiece(pieceID int) (*torrent.FileEntry, int, error) {
	offset := pieceID * w.info.PieceLength
	for id, f := range w.files {
		if offset <= f.EndOffset {
			if err := f.OpenFile(); err != nil {
				return nil, 0, err
			}
			return f, id, nil
		}
	}
	return nil, 0, fmt.Errorf("failed to find the file for piece index: %d", pieceID)
}

func (w *PieceWriter) writePiece(pieceID int, piece []byte) (n int, err error) {
	defer func() {
		if err == nil {
			w.log.Println("[DOWNLOAD PIECE]", "piece_index", pieceID)
		}
	}()

	entry, fileID, err := w.entryForPiece(pieceID)
	if err != nil {
		return 0, err
	}

	pieceOffset := pieceID * w.info.PieceLength
	entryOffset := pieceOffset - entry.StartOffset

	if entryOffset+len(piece) > entry.Length {
		diff := entry.Length - entryOffset
		start := piece[:diff]
		remainder := piece[diff:]
		remainderLen := len(remainder)

		if _, err := entry.File.WriteAt(start, int64(entryOffset)); err != nil {
			return 0, err
		}

		for remainderLen > 0 {
			fileID++
			entry = w.files[fileID]
			if err := entry.OpenFile(); err != nil {
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

	return entry.File.WriteAt(piece, int64(entryOffset))
}
