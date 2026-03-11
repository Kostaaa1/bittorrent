package main

import (
	"context"
	"crypto/rand"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	logger "test/internal/log"
	"test/internal/torrent"
	"test/internal/torrent/client"
	"test/internal/torrent/peer"
)

func getClientID() ([20]byte, error) {
	buf := [20]byte{}
	_, err := rand.Read(buf[:])
	if err != nil {
		return buf, err
	}
	return buf, nil
}

func init() {
	go func() {
		http.ListenAndServe("localhost:6060", nil)
	}()
}

func main() {
	if len(os.Args) <= 1 {
		panic("input missing")
	}

	input := os.Args[1]

	tf, err := torrent.NewFile(input)
	if err != nil {
		log.Fatal(err)
	}
	tf.Print()

	clientID, err := getClientID()
	if err != nil {
		log.Fatal(err)
	}
	var port uint16 = 6881

	NumBlocksPerPiece := tf.PieceLength / peer.MaxBlockSize

	info := &torrent.TorrentInfo{
		InfoHash:          tf.InfoHash,
		NumOfPieces:       len(tf.Pieces),
		TotalLength:       tf.TotalLength,
		PieceLength:       tf.PieceLength,
		BlockSize:         peer.MaxBlockSize,
		NumBlocksPerPiece: NumBlocksPerPiece,
	}

	// log := newLogger()
	// f, _ := os.OpenFile("traffic.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	// f := os.Stdout
	// f := io.Discard

	log := logger.New(os.Stdout)
	c := client.New(clientID, port, info, tf.Pieces, tf.Files, tf.Announce, tf.AnnounceList, log)
	ctx := context.Background()

	c.Run(ctx)
}
