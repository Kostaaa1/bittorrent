package main

import (
	"os"
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
		panic(nil)
	}
	defer f.Close()

	// v, err := bencode.Decode(f)
	// if err != nil {
	// 	panic(err)
	// }

	// v, err := bencode.NewDecoder(f).Decode()
	// if err != nil {
	// 	panic(err)
	// }
	// fmt.Println(v)
}
