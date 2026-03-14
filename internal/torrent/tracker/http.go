package tracker

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Kostaaa1/bittorrent/pkg/bencode"
)

type AnnounceResponse struct {
	FailureReason  string `bencode:"failure reason"`
	WarningMessage string `bencode:"warning reason"`
	Interval       int    `bencode:"interval"`
	MinInterval    int    `bencode:"min interval"`
	TrackerID      string `bencode:"tracker id"`
	Complete       int    `bencode:"complete"`
	Incomplete     int    `bencode:"incomplete"`
	Peers6         string `bencode:"peers6"`
	Peers          Peers  `bencode:"peers"`
}

type PeerAddress struct {
	PeerID string `bencode:"peer id"`
	IP     string `bencode:"ip"`
	Port   uint16 `bencode:"port"`
}

func (a *PeerAddress) IP4Addr() string {
	return fmt.Sprintf("%s:%d", a.IP, a.Port)
}

type Peers struct {
	Binary []byte
	List   []PeerAddress
}

func (p *Peers) UnmarshalBencode(d *bencode.Decoder) error {
	switch d.PeekByte() {
	case bencode.KindList:
		if err := d.Decode(&p.List); err != nil {
			return err
		}
	default:
		if err := d.Decode(&p.Binary); err != nil {
			return err
		}
	}
	return nil
}

type AnnounceRequest struct {
	Tracker string
	// info_hash, key, peer_id, port, downloaded, left, uploaded and compact
	InfoHash   [20]byte
	PeerID     [20]byte
	Port       uint16
	Uploaded   uint64
	Downloaded uint64
	Left       uint64
	Compact    bool
	NoPeerID   bool // ignored if compact is enabled
	// started: The first request to the tracker must include the event key with this value.
	// stopped: Must be sent to the tracker if the client is shutting down gracefully.
	// completed: Must be sent to the tracker when the download completes. However, must not be sent if the download was already 100% complete when the client started. Presumably, this is to allow the tracker to increment the "completed downloads" metric based solely on this event.
	Event string // "started, completed, stopped"
	// IP *string
	NumWant   int32
	Key       *string
	TrackerID *string
	// key: Optional. An additional identification that is not shared with any other peers. It is intended to allow a client to prove their identity should their IP address change.
	// trackerid: Optional. If a previous announce contained a tracker id, it should be set here.
}

func BuildHTTPTrackerURL(req AnnounceRequest) (string, error) {
	parsed, err := url.Parse(req.Tracker)
	if err != nil {
		return "", err
	}

	v := url.Values{
		"info_hash":  []string{string(req.InfoHash[:])},
		"peer_id":    []string{string(req.PeerID[:])},
		"port":       []string{strconv.Itoa(int(req.Port))},
		"uploaded":   []string{strconv.Itoa(int(req.Uploaded))},
		"downloaded": []string{strconv.Itoa(int(req.Downloaded))},
		"left":       []string{strconv.Itoa(int(req.Left))},
		"corrupt":    []string{"0"},
		// "key":        []string{*req.Key},
		// "event":      []string{"started"},
		"numwant":    []string{"100"},
		"compact":    []string{"1"},
		"no_peer_id": []string{"1"},
	}

	parsed.RawQuery = v.Encode()

	return parsed.String(), nil
}

func Announce(trackerURL string) (*AnnounceResponse, error) {
	resp, err := http.Get(trackerURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res AnnounceResponse
	if err := bencode.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	if res.FailureReason != "" {
		return nil, fmt.Errorf("failed to get the peers from HTTP tracker=%s, error=%s", trackerURL, res.FailureReason)
	}

	return &res, nil
}

func ParsePeers(res AnnounceResponse) ([]PeerAddress, error) {
	p := res.Peers.List
	if len(res.Peers.Binary) > 0 {
		v, err := parsePeersBinary(res.Peers.Binary)
		if err != nil {
			return nil, err
		}
		p = v
	}
	return p, nil
}

func parsePeersBinary(peers []byte) ([]PeerAddress, error) {
	if len(peers)%6 != 0 {
		return nil, fmt.Errorf("peers received in wrong format: not divisible by 6 - %d", len(peers))
	}

	parsed := make([]PeerAddress, len(peers)/6)

	for i := range parsed {
		b := [6]byte{}
		copy(b[:], peers[i*6:i*6+6])
		parsed[i] = PeerAddress{
			IP:   fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3]),
			Port: binary.BigEndian.Uint16([]byte{b[4], b[5]}),
		}
	}

	return parsed, nil
}
