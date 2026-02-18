package tracker

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

type AnnEvent string

const (
	EventStarted   AnnEvent = "started"
	EventCompleted AnnEvent = "completed"
	EventStopped   AnnEvent = "stopped"
	EventNone      AnnEvent = ""
)

type Announcer struct {
	infoHash       [20]byte
	clientID       [20]byte
	port           uint16
	downloaded     atomic.Uint64
	left           atomic.Uint64
	uploaded       atomic.Uint64
	active         int
	peerCh         chan<- PeerAddress
	announce       string
	announceList   [][]string
	isMultiTracker bool
}

func NewAnnouncer(
	infoHash,
	clientID [20]byte,
	port uint16,
	left uint64,
	peerCh chan<- PeerAddress,
	announce string,
	announceList [][]string,
	isMultiTracker bool,
) *Announcer {
	ann := &Announcer{
		isMultiTracker: isMultiTracker,
		infoHash:       infoHash,
		clientID:       clientID,
		port:           port,
		peerCh:         peerCh,
		announce:       announce,
		announceList:   announceList,
	}
	ann.left.Store(left)
	return ann
}

func (ann *Announcer) IncDownloaded(n uint64) {
	ann.left.Add(-n)
	ann.downloaded.Add(n)
}

func (ann *Announcer) IncUploaded(n uint64) {
	ann.uploaded.Add(n)
}

// if single mode find first tracker that responds with peers and use it for announcing - spawn single goroutine
// if multi mode, send request to all trackers, if they respond, spawn goroutine and announce it periodically
func (announcer *Announcer) Run(ctx context.Context) {
	if len(announcer.announceList) > 0 {
		for _, tier := range announcer.announceList {
			for _, announce := range tier {
				if strings.HasPrefix(announce, "http") {
					continue
				}

				interval, err := announcer.run(announce)
				if err != nil {
					fmt.Println("failed to get ther response for announce announce:", announce, err)
					continue
				}

				if !announcer.isMultiTracker && announcer.active > 0 {
					return
				}
				announcer.active++

				if interval == 0 {
					interval = 600
				}

				go func() {
					ticker := time.NewTicker(time.Duration(interval) * time.Second)
					for {
						select {
						case <-ctx.Done():
							ticker.Stop()
							return
						case <-ticker.C:
							_, err := announcer.run(announce)
							if err != nil {
								fmt.Println("failed tick announce:", err)
							}
						}
					}
				}()
			}
		}
	} else {
		tracker := announcer.announce

		interval, err := announcer.run(tracker)
		if err != nil {
			fmt.Println("failed to get the response for announce tracker:", tracker, err)
			return
		}

		if interval == 0 {
			interval = 600
		}

		go func() {
			ticker := time.NewTicker(time.Duration(interval) * time.Second)
			for {
				select {
				case <-ctx.Done():
					ticker.Stop()
					return
				case <-ticker.C:
					_, err := announcer.run(tracker)
					if err != nil {
						fmt.Println("failed tick announce:", err)
					}
				}
			}
		}()
	}
}

func (ann *Announcer) run(tracker string) (uint32, error) {
	fmt.Println("SENDING ANNOUNCE REQUEST", tracker)

	annReq := AnnounceRequest{
		Tracker:    tracker,
		InfoHash:   ann.infoHash,
		PeerID:     ann.clientID,
		Port:       ann.port,
		Compact:    true,
		Left:       ann.left.Load(),
		Uploaded:   ann.uploaded.Load(),
		Downloaded: ann.downloaded.Load(),
		NumWant:    100,
	}

	if strings.HasPrefix(tracker, "http") {
		url, err := BuildHTTPTrackerURL(annReq)
		if err != nil {
			return 0, err
		}
		resp, err := Announce(url)
		if err != nil {
			return 0, err
		}
		peers, err := ParsePeers(*resp)
		if err != nil {
			return 0, err
		}
		for _, peer := range peers {
			ann.peerCh <- peer
		}
		return uint32(resp.MinInterval), nil
	}

	if strings.HasPrefix(tracker, "udp") {
		conn, err := DialUDPTracker(tracker)
		if err != nil {
			return 0, err
		}
		defer conn.conn.Close()

		connID, err := conn.Connect()
		if err != nil {
			return 0, err
		}

		interval, peers, err := conn.Announce(connID, annReq)
		if err != nil {
			return 0, err
		}

		for _, peer := range peers {
			ann.peerCh <- peer
		}

		return interval, nil
	}

	return 0, nil
}
