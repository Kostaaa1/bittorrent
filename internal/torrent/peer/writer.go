package peer

import (
	"crypto/sha1"
	"fmt"
	"log"
	"math"
	"os"

	logger "github.com/Kostaaa1/bittorrent/internal/log"
	"github.com/Kostaaa1/bittorrent/internal/torrent"
)

type Result struct {
	Index    int
	Begin    int
	LenBlock int
	Err      error
}

type pieceBuffer struct {
	index       int
	size        int
	hash        [20]byte
	buffer      []byte
	totalBlocks int
	blockCount  int
}

func (pb *pieceBuffer) verify() bool { return sha1.Sum(pb.buffer) == pb.hash }

func (pb *pieceBuffer) writeBlock(begin int, block []byte) {
	// if len(pb.blockOffsets) < pb.size {
	// pb.blockOffsets = append(pb.blockOffsets, begin)
	// }
	copy(pb.buffer[begin:], block)
	pb.blockCount++
}

type PieceWriter struct {
	info         *torrent.TorrentInfo
	pieceBuffers map[int]*pieceBuffer
	files        []*torrent.FileEntry
	worker       chan PieceMessage
	results      chan Result
	log          *log.Logger
	slog         *logger.Log
	hashes       [][20]byte
}

func NewPieceWriter(
	info *torrent.TorrentInfo,
	hashes [][20]byte,
	entries []*torrent.FileEntry,
	slog *logger.Log,
) *PieceWriter {
	f, _ := os.OpenFile("writes.log", os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)
	log := log.New(f, "", log.Ltime)
	return &PieceWriter{
		hashes:       hashes,
		worker:       make(chan PieceMessage),
		results:      make(chan Result),
		pieceBuffers: make(map[int]*pieceBuffer),
		files:        entries,
		info:         info,
		log:          log,
		slog:         slog,
	}
}

func (pw *PieceWriter) Channels() (chan PieceMessage, chan Result) {
	return pw.worker, pw.results
}

func (pw *PieceWriter) newPieceBuffer(piece int) *pieceBuffer {
	size := pw.info.PieceLength
	blocks := pw.info.NumBlocksPerPiece
	lastPiece := pw.info.NumOfPieces - 1

	if piece == lastPiece {
		size = pw.info.TotalLength - (lastPiece * pw.info.PieceLength)
		blocks = int(math.Ceil(float64(size) / float64(pw.info.BlockSize)))
	}

	return &pieceBuffer{
		index:       piece,
		hash:        pw.hashes[piece],
		totalBlocks: blocks,
		buffer:      make([]byte, size),
		size:        size,
	}
}

func (pw *PieceWriter) writeBlockToBuffer(msg PieceMessage) *pieceBuffer {
	buf := pw.pieceBuffers[msg.Index]
	if buf == nil {
		buf = pw.newPieceBuffer(msg.Index)
	}
	buf.writeBlock(msg.Begin, msg.Block)
	pw.pieceBuffers[msg.Index] = buf
	return buf
}

func (pw *PieceWriter) Run() error {
	pw.slog.Write("[WRITER] started", "num_pieces", pw.info.NumOfPieces)
	defer pw.slog.Write("[WRITER] stopped: worker channel closed",
		"open_buffers", len(pw.pieceBuffers))

	for msg := range pw.worker {
		piece := pw.writeBlockToBuffer(msg)

		pw.slog.Write("[WRITER] block buffered",
			"piece", msg.Index,
			"begin", msg.Begin,
			"block_len", len(msg.Block),
			"block_count", piece.blockCount,
			"total_blocks", piece.totalBlocks,
			"piece_size", piece.size,
			"open_buffers", len(pw.pieceBuffers),
		)

		if piece.blockCount == piece.totalBlocks {
			result := Result{
				Index:    msg.Index,
				Begin:    msg.Begin,
				LenBlock: len(msg.Block),
			}

			pw.slog.Write("[WRITER] piece complete, verifying",
				"piece", msg.Index,
				"block_count", piece.blockCount,
				"total_blocks", piece.totalBlocks,
				"piece_size", piece.size,
			)

			if !piece.verify() {
				result.Err = fmt.Errorf("hashes do not match for piece %d", msg.Index)
				pw.slog.Write("[WRITER] HASH MISMATCH",
					"piece", msg.Index,
					"buffer_index", piece.index,
					"piece_size", piece.size,
					"block_count", piece.blockCount,
					"total_blocks", piece.totalBlocks,
					"got", fmt.Sprintf("%x", sha1.Sum(piece.buffer)),
					"want", fmt.Sprintf("%x", piece.hash),
				)
				fmt.Println("TARGET:", msg.Index, piece.index)
				fmt.Println("Block:", sha1.Sum(msg.Block))
				fmt.Println("Buffer:", sha1.Sum(piece.buffer))
				fmt.Println("Match hash piece:", piece.hash)
				// for _, hash := range pw.hashPieces {
				// 	fmt.Println(hash)
				// }
			} else if _, err := pw.writePiece(msg.Index, piece.buffer); err != nil {
				result.Err = err
				pw.slog.Write("[WRITER] write to disk FAILED",
					"piece", msg.Index, "err", err)
			} else {
				pw.slog.Write("[WRITER] piece verified + written to disk",
					"piece", msg.Index, "piece_size", piece.size)
			}

			delete(pw.pieceBuffers, piece.index)

			pw.slog.Write("[WRITER] sending result -> resultCh (may block)",
				"piece", result.Index, "err", result.Err, "open_buffers", len(pw.pieceBuffers))

			pw.results <- result

			pw.slog.Write("[WRITER] result delivered", "piece", result.Index)
		}
	}

	return nil
}

func (w *PieceWriter) entryForPiece(piece int) (*torrent.FileEntry, int, error) {
	offset := piece * w.info.PieceLength
	for id, f := range w.files {
		if offset <= f.EndOffset {
			if err := f.OpenFile(); err != nil {
				return nil, 0, err
			}
			return f, id, nil
		}
	}
	return nil, 0, fmt.Errorf("failed to find the file for piece index: %d", piece)
}

func (w *PieceWriter) writePiece(piece int, data []byte) (n int, err error) {
	defer func() {
		if err == nil {
			w.log.Println("[DOWNLOAD PIECE]", "piece_index", piece)
		}
	}()

	entry, fileID, err := w.entryForPiece(piece)
	if err != nil {
		return 0, err
	}

	pieceOffset := piece * w.info.PieceLength
	entryOffset := pieceOffset - entry.StartOffset

	if entryOffset+len(data) > entry.Length {
		diff := entry.Length - entryOffset
		start := data[:diff]
		remainder := data[diff:]
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

		return len(data), nil
	}

	return entry.File.WriteAt(data, int64(entryOffset))
}
