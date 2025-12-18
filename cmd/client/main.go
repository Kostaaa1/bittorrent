package main

import (
	"fmt"
	"os"
	"test/pkg/bencode"
)

type TorrentFile struct {
	Announce     string   `bencode:"announce"`
	AnnounceList []string `bencode:"announce-list"`
	Info         Info     `bencode:"info"`
	CreationDate int      `bencode:"creation date"`
	Encoding     string   `bencode:"encoding"`
	Publisher    string   `bencode:"publisher"`
	PublisherURL string   `bencode:"publisher url"`
}

type Info struct {
	Length      int    `bencode:"length"`
	Name        string `bencode:"name"`
	PieceLength int    `bencode:"piece length"`
	Pieces      string `bencode:"pieces"`
}

func main() {
	f, err := os.Open("file.torrent")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	var v TorrentFile
	if err := bencode.NewDecoder(f).Decode(&v); err != nil {
		panic(err)
	}
	fmt.Println(v)
}
