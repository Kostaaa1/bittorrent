package main

import (
	"crypto/rand"
	"log"
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
	tf, err := torrent.NewFile("test_torrents/ml.torrent")
	if err != nil {
		log.Fatal(err)
	}
	tf.Print()

	var port uint16 = 6881

	clientID, err := getClientID()
	if err != nil {
		log.Fatal(err)
	}

	if err := tf.Download(clientID, port); err != nil {
		log.Fatal(err)
	}
}
