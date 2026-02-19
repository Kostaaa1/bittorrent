package tracker

import (
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/url"
	"time"
)

type action int

const (
	ActionConnect action = iota
	ActionAnnounce
	ActionScrape
	ActionError
)

type udpTracker struct {
	conn *net.UDPConn
}

func DialUDPTracker(trackerURL string) (*udpTracker, error) {
	parsed, err := url.Parse(trackerURL)
	if err != nil {
		return nil, err
	}

	addr, err := net.ResolveUDPAddr("udp", parsed.Host)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}

	conn.SetReadDeadline(time.Now().Add(time.Second * 5))

	return &udpTracker{conn}, nil
}

func (u *udpTracker) sendConnect(txID uint32) error {
	const protocolID = 0x41727101980
	connectMsg := make([]byte, 16)
	magicConstant := uint64(protocolID)
	binary.BigEndian.PutUint64(connectMsg[:8], magicConstant)           // protocol_id
	binary.BigEndian.PutUint32(connectMsg[8:12], uint32(ActionConnect)) // action 0
	binary.BigEndian.PutUint32(connectMsg[12:16], txID)                 // transaction id

	_, err := u.conn.Write(connectMsg)
	if err != nil {
		return err
	}

	return nil
}

func (u *udpTracker) readConnect(txID uint32) (uint64, error) {
	connResp := make([]byte, 16)

	_, err := io.ReadFull(u.conn, connResp)
	if err != nil {
		return 0, err
	}

	action := binary.BigEndian.Uint32(connResp[:4])
	if action != uint32(ActionConnect) {
		return 0, errors.New("failed connect: did not receive CONNECT action")
	}

	respTxID := binary.BigEndian.Uint32(connResp[4:8])
	if respTxID != txID {
		return 0, errors.New("failed connect: transaction_id's do not match")
	}

	return binary.BigEndian.Uint64(connResp[8:16]), nil
}

func (u *udpTracker) Connect() (uint64, error) {
	txID := rand.Uint32()
	if err := u.sendConnect(txID); err != nil {
		return 0, err
	}
	return u.readConnect(txID)
}

func (u *udpTracker) Announce(connID uint64, req AnnounceRequest) (uint32, []PeerAddress, error) {
	txID, packet := req.udpBytes(connID)

	_, err := u.conn.Write(packet)
	if err != nil {
		return 0, nil, err
	}

	buf := make([]byte, 2048)
	n, err := u.conn.Read(buf)
	if err != nil {
		return 0, nil, err
	}

	if n < 20 {
		return 0, nil, errors.New("failed announce: announce response packet is less then 20 bytes")
	}

	annResp := buf[:n]

	action := binary.BigEndian.Uint32(annResp[:4])
	if action != uint32(ActionAnnounce) {
		return 0, nil, errors.New("failed connect: did not receive ANNOUNCE action")
	}

	transactionID := binary.BigEndian.Uint32(annResp[4:8])
	if transactionID != txID {
		return 0, nil, errors.New("failed connect: transaction_id's do not match")
	}

	interval := binary.BigEndian.Uint32(annResp[8:12])
	leechers := binary.BigEndian.Uint32(annResp[12:16])
	seeders := binary.BigEndian.Uint32(annResp[16:20])
	_ = leechers
	_ = seeders
	_ = interval

	peers, err := parsePeersBinary(annResp[20:])
	if err != nil {
		return 0, nil, err
	}

	return interval, peers, nil
}

func (req AnnounceRequest) udpBytes(connID uint64) (uint32, []byte) {
	txID := rand.Uint32()
	request := make([]byte, 98)
	binary.BigEndian.PutUint64(request[:8], connID)
	binary.BigEndian.PutUint32(request[8:12], uint32(ActionAnnounce)) // action
	binary.BigEndian.PutUint32(request[12:16], txID)                  // transaction id (4 byte)
	copy(request[16:36], req.InfoHash[:])                             // info hash (20 bytes)
	copy(request[36:56], req.PeerID[:])                               // peer id (20 bytes)
	binary.BigEndian.PutUint64(request[56:64], req.Downloaded)        // downloaded
	binary.BigEndian.PutUint64(request[64:72], req.Left)              // left
	binary.BigEndian.PutUint64(request[72:80], req.Uploaded)          // uploaded
	binary.BigEndian.PutUint32(request[80:84], uint32(2))             // event - 0: none; 1: completed; 2: started; 3: stopped
	binary.BigEndian.PutUint32(request[84:88], uint32(0))             // ip address - 0 default
	binary.BigEndian.PutUint32(request[88:92], uint32(0))             // key -
	binary.BigEndian.PutUint32(request[92:96], uint32(req.NumWant))   // default -1
	binary.BigEndian.PutUint16(request[96:98], req.Port)              // uint16
	return txID, request
}
