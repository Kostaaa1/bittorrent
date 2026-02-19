package main

import (
	"context"
	"crypto/rand"
	"log"
	"os"
	"test/internal/torrent"
)

func getClientID() ([20]byte, error) {
	buf := [20]byte{}
	_, err := rand.Read(buf[:])
	if err != nil {
		return buf, err
	}
	return buf, nil
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

	ctx := context.Background()

	if err := tf.Download(ctx, clientID, port); err != nil {
		log.Fatal(err)
	}
}
