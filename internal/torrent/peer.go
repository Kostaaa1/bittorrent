package torrent

import (
	"fmt"
	"io"
	"math"
	"net"
	"time"
)

func (p *Peer) writeMsg(msg Message) error {
	if _, err := p.conn.Write(msg.Bytes()); err != nil {
		return err
	}
	return nil
}
func (p *Peer) sendChoke() error {
	p.amChoking = true
	return p.writeMsg(Message{ID: MsgChoke})
}
func (p *Peer) sendUnchoke() error {
	p.amChoking = false
	return p.writeMsg(Message{ID: MsgUnchoke})
}
func (p *Peer) sendInterested() error {
	p.amInterested = true
	return p.writeMsg(Message{ID: MsgInterested})
}
func (p *Peer) sendUninterested() error {
	p.amInterested = false
	return p.writeMsg(Message{ID: MsgUninterested})
}
func (p *Peer) sendHave() error {
	// return p.writeMsg(Message{ID: MsgHave, Payload: []byte()})
	return nil
}
func (p *Peer) sendRequest(index, begin, block int) error {
	// TODO: before requesting, we need to ask the peer if it has that piece
	fmt.Println("[REQUEST] request piece:", index, begin, block)
	payload := FormatRequest(index, begin, block)
	return p.writeMsg(Message{ID: MsgRequest, Payload: payload})
}

type Peer struct {
	addr     string
	conn     net.Conn
	bitfield []byte

	// peer state
	amChoking      bool
	amInterested   bool
	peerChoking    bool
	peerInterested bool

	writer   *PieceWriter
	pipeline *requestPipeline

	numOfPieces       int
	totalLength       int
	pieceLength       int
	blockSize         int
	numBlocksPerPiece int
}

type requestPipeline struct {
	reqSignal  chan struct{}
	windowSize int
}

func (peer *Peer) runPipeline() {
	for i := 0; i < peer.pipeline.windowSize; i++ {
		peer.pipeline.reqSignal <- struct{}{}
	}

	lastPieceID := peer.numOfPieces - 1
	lastBlockID := peer.pieceLength - peer.blockSize
	currPiece := 0
	currOffset := 0

	// TODO: temporary
	done := false

	for {
		if !done && peer.canRequest() {
			<-peer.pipeline.reqSignal

			if currOffset == lastBlockID {
				if currPiece == lastPieceID {
					remainder := peer.totalLength - currOffset - (lastPieceID * peer.pieceLength)
					peer.sendRequest(currPiece, currOffset, remainder)
					done = true
				} else {
					peer.sendRequest(currPiece, currOffset, peer.blockSize)
					currPiece += 1
					currOffset = 0
				}
			} else {
				peer.sendRequest(currPiece, currOffset, peer.blockSize)
				currOffset += peer.blockSize
			}
		}
	}
}

func (p *Peer) canRequest() bool {
	return p.amInterested && !p.peerChoking
}

func (p *Peer) Close() error {
	return p.conn.Close()
}

func (p *Peer) readMessages() error {
	for {
		msg, err := ReadMessage(p.conn)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		switch msg.ID {
		case MsgChoke:
			// clear pending requests
			// notify piece Worker that pending requests are dead
			fmt.Println("[MESSAGE] choke")
			p.peerChoking = true
		case MsgUnchoke:
			fmt.Println("[MESSAGE] unchoke")
			p.peerChoking = false
		case MsgInterested:
			fmt.Println("[MESSAGE] interested")
			p.peerInterested = true
		case MsgUninterested:
			fmt.Println("[MESSAGE] uninterested")
			p.peerInterested = false
		case MsgBitfield:
			fmt.Println("[MESSAGE] bitfield", msg.Payload)
			p.bitfield = msg.Payload
		case MsgRequest:
			fmt.Println("[MESSAGE] request")
			p.bitfield = msg.Payload
		case MsgPiece:
			piece := ParsePieceMessage(msg.Payload)
			fmt.Printf("[PIECE] index %d, begin %d, blocks %d\n", piece.index, piece.begin, len(piece.block))
			p.pipeline.reqSignal <- struct{}{}
			p.writer.worker <- piece
		case MsgHave:
			fmt.Println("[MESSAGE] have")
		case MsgCancel:
			fmt.Println("[MESSAGE] cancel")
		case MsgPort:
			fmt.Println("[MESSAGE] port")
		}
	}
}

func (p *Peer) handshake(hs *Handshake) error {
	_, err := p.conn.Write(hs.Bytes())
	if err != nil {
		return fmt.Errorf("failed to write handshake: %v", err)
	}
	peerHandshake, err := ReadHandshake(p.conn)
	if err != nil {
		return fmt.Errorf("failed to read handshake: %v", err)
	}
	if peerHandshake.InfoHash != hs.InfoHash {
		return fmt.Errorf("failed to validate handshakes for peer: %s", p.conn.RemoteAddr())
	}
	return nil
}

func DialPeer(addr string, hs *Handshake, tf *TorrentFile) error {
	fmt.Println("Dialing peer:", addr)

	conn, err := net.DialTimeout("tcp", addr, time.Second*30)
	if err != nil {
		return fmt.Errorf("failed to dial: %v", err)
	}

	blockSize := int(math.Pow(2, 14))

	peer := &Peer{
		conn:              conn,
		amInterested:      false,
		peerChoking:       true,
		numOfPieces:       len(tf.Pieces),
		totalLength:       tf.TotalLength,
		pieceLength:       tf.PieceLength,
		blockSize:         blockSize,
		numBlocksPerPiece: tf.PieceLength / blockSize,
	}

	peer.conn = conn
	if err := peer.handshake(hs); err != nil {
		fmt.Println("Handshake failed:", err)
		if err := conn.Close(); err != nil {
			return err
		}
		peer.conn = nil
		return err
	}

	fmt.Println("Handshake successful with peer:", addr)

	peer.writer = &PieceWriter{
		blockSize:         peer.blockSize,
		numBlocksPerPiece: peer.numBlocksPerPiece,
		pieceLength:       peer.pieceLength,
		totalLength:       peer.totalLength,
		hashedPieces:      tf.Pieces,
		numWorkers:        3,
		worker:            make(chan PieceMessage),
		pieces:            make(map[int]*piece),
		files:             tf.Files,
	}

	peer.pipeline = &requestPipeline{
		windowSize: 4,
		reqSignal:  make(chan struct{}, 4),
	}

	if err := peer.sendInterested(); err != nil {
		return err
	}

	go peer.runPipeline()
	go peer.writer.Start()

	return peer.readMessages()
}
