package main

import (
	"bytes"
	"fmt"

	"github.com/jackpal/bencode-go"
)

type TorrentFile struct {
	Announce     string `bencode:"announce"`
	Info         Info   `bencode:"info"`
	CreationDate int    `bencode:"creation date"`
	Encoding     string `bencode:"encoding"`
	Publisher    string `bencode:"publisher"`
	PublisherURL string `bencode:"publisher url"`
}

type Info struct {
	Length      int    `bencode:"length"`
	Name        string `bencode:"name"`
	PieceLength int    `bencode:"piece length"`
	Pieces      string `bencode:"pieces"`
}

func main() {
	// tf := TorrentFile{
	// 	Announce:     "test123",
	// 	CreationDate: int(time.Now().Unix()),
	// 	Encoding:     "UTF-8",
	// 	Publisher:    "who",
	// 	PublisherURL: "http://who.com",
	// }

	// t := reflect.TypeOf(tf)

	// for i := 0; i < t.NumField(); i++ {
	// 	field := t.Field(i)
	// 	fmt.Println(field)
	// }

	buf := bytes.NewBuffer([]byte(
		"d8:announce13:http://x/7:comment12:test torrent13:creation datei1700000000e4:infod6:lengthi5e4:name4:test12:piece lengthi5e6:pieces20:bbbbbbbbbbbbbbbbbbbbee",
	))
	v, err := bencode.Decode(buf)
	if err != nil {
		panic(err)
	}
	fmt.Println(v)
}
