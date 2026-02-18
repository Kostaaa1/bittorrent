package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"strings"
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

	udpTracker := ""

	for _, tier := range tf.AnnounceList {
		for _, tracker := range tier {
			if strings.HasPrefix(tracker, "udp") {
				udpTracker = tracker
				break
			}
		}
		if udpTracker != "" {
			break
		}
	}

	fmt.Println("found", udpTracker)

	clientID, err := getClientID()
	if err != nil {
		log.Fatal(err)
	}
	// var port uint16 = 6881

	addr, err := net.ResolveUDPAddr("udp", "open.stealth.si:80")
	if err != nil {
		panic(err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	connectMsg := make([]byte, 16)

	magicConstant := uint64(0x41727101980)
	binary.BigEndian.PutUint64(connectMsg[:8], magicConstant)
	binary.BigEndian.PutUint32(connectMsg[8:12], 0)
	binary.BigEndian.PutUint32(connectMsg[12:16], rand.Uint32())

	fmt.Println(connectMsg)
	fmt.Println("TRANSACTION ID:", connectMsg[12:16])

	n, err := conn.Write(connectMsg)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("WRITTEN", n)

	buf := make([]byte, 16)
	n, err = io.ReadFull(conn, buf)
	if err != nil {
		log.Fatal(err)
	}

	readAction := buf[:4]
	readTransactionID := buf[4:8]
	connID := buf[8:16]

	fmt.Println("RESPONSE:", readAction, readTransactionID, connID)

	request := make([]byte, 98)
	n, err = binary.Encode(request, binary.BigEndian, connID)
	if err != nil {
		log.Fatal(err)
	}

	binary.BigEndian.PutUint32(request[8:12], 1)

	// binary.BigEndian.PutUint32(request[8:12], 1)
	n, err = binary.Encode(request[16:36], binary.BigEndian, tf.InfoHash)
	if err != nil {
		log.Fatal(err)
	}
	n, err = binary.Encode(request[36:56], binary.BigEndian, clientID)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("ENCODEDr:", request)

	// listener, err := net.Listen("tcp", ":6881")
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// go func() {
	// 	for {
	// 		conn, err := listener.Accept()
	// 		if err != nil {
	// 			fmt.Println(err)
	// 			continue
	// 		}
	// 		b, err := io.ReadAll(conn)
	// 		if err != nil {
	// 			log.Fatal(err)
	// 		}
	// 		fmt.Println("RECEIVED FROM CONNECTION", string(b))
	// 		fmt.Println("RECEIVED FROM CONNECTION", string(b))
	// 		fmt.Println("RECEIVED FROM CONNECTION", string(b))
	// 		fmt.Println("RECEIVED FROM CONNECTION", string(b))
	// 		fmt.Println("RECEIVED FROM CONNECTION", string(b))
	// 		fmt.Println("RECEIVED FROM CONNECTION", string(b))
	// 	}
	// }()

	// ctx := context.Background()

	// if err := tf.Download(ctx, clientID, port); err != nil {
	// 	log.Fatal(err)
	// }
}
