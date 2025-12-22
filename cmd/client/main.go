package main

import (
	"fmt"
	"log"
	"os"
	"test/pkg/bencode"
)

type bencodeTorrent struct {
	Announce  string      `bencode:"announce"`
	InfoHash  bencodeInfo `bencode:"info"`
	TestNoTag string
	testAnon  string
}

type bencodeInfo struct {
	Name        string `bencode:"name"`
	Length      int    `bencode:"length"`
	PieceLength int    `bencode:"piece length"`
	Pieces      string `bencode:"pieces"`
}

// func (i bencodeInfo) hash() [20]byte {
// 	sha1.Sum()
// }

type TorrentFile struct {
	Announce    string
	Length      int
	Name        string
	PieceLength int
	Pieces      [][20]byte
	InfoHash    [20]byte
}

func (bto *bencodeTorrent) toTorrentFile() (*TorrentFile, error) {
	return &TorrentFile{
		Announce:    bto.Announce,
		Name:        bto.InfoHash.Name,
		Length:      bto.InfoHash.Length,
		PieceLength: bto.InfoHash.PieceLength,
	}, nil
}

func main() {
	f, err := os.Open("file.torrent")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	var src bencodeTorrent
	if err := bencode.NewDecoder(f).Decode(&src); err != nil {
		log.Fatal(err)
	}

	fmt.Println("SRC:", src)

	// panic: json: cannot unmarshal number 2500 into Go struct field t.completed.title of type int8
	//
	//	type t struct {
	//		UserID int  `json:"userId"`
	//		ID     int  `json:"id"`
	//		Title  int8 `json:"title"`
	//	}
	//
	//	type Test struct {
	//		UserID    int    `json:"userId"`
	//		ID        int    `json:"id"`
	//		Title     string `json:"title"`
	//		Completed t      `json:"completed"`
	//	}
	//
	//	da := []byte(`{
	//	  "userId": 1,
	//	  "id": 1,
	//	  "title": "delectus aut autem",
	//	  "completed": {
	//	    "userId": 1,
	//	    "id": 1,
	//	    "title": 2500
	//	  }
	//	}`)
	//
	// var test Test
	//
	//	if err := json.Unmarshal(da, &test); err != nil {
	//		panic(err)
	//	}
	//
	// fmt.Println(test)
}
