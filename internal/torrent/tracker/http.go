package tracker

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"test/pkg/bencode"
)

type HTTPTrackerResponse struct {
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

type PeerInfo struct {
	PeerID string `bencode:"peer id"`
	IP     string `bencode:"ip"`
	Port   uint16 `bencode:"port"`
}

type Peers struct {
	Binary []byte
	List   []PeerInfo
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

type HTTPTrackerRequestParams struct {
	Tracker string
	// info_hash, key, peer_id, port, downloaded, left, uploaded and compact
	InfoHash   [20]byte
	PeerID     [20]byte
	Port       uint16 // typically 6881-6889
	Uploaded   int
	Downloaded int
	Left       int
	Compact    int8
	NoPeerID   int8 // ignored if compact is enabled
	// started: The first request to the tracker must include the event key with this value.
	// stopped: Must be sent to the tracker if the client is shutting down gracefully.
	// completed: Must be sent to the tracker when the download completes. However, must not be sent if the download was already 100% complete when the client started. Presumably, this is to allow the tracker to increment the "completed downloads" metric based solely on this event.
	Event string // "started, completed, stopped"
	// IP *string
	NumWant   uint64
	Key       *string
	TrackerID *string
	// key: Optional. An additional identification that is not shared with any other peers. It is intended to allow a client to prove their identity should their IP address change.
	// trackerid: Optional. If a previous announce contained a tracker id, it should be set here.
}

func DiscoverPeers(params HTTPTrackerRequestParams) ([]PeerInfo, error) {
	u, err := buildHTTPTrackerURL(params)
	if err != nil {
		return nil, err
	}
	return requestPeers(u)
}

func buildHTTPTrackerURL(p HTTPTrackerRequestParams) (string, error) {
	parsed, err := url.Parse(p.Tracker)
	if err != nil {
		return "", err
	}

	v := url.Values{
		"info_hash":  []string{string(p.InfoHash[:])},
		"peer_id":    []string{string(p.PeerID[:])},
		"port":       []string{strconv.Itoa(int(p.Port))},
		"uploaded":   []string{strconv.Itoa(p.Uploaded)},
		"downloaded": []string{strconv.Itoa(p.Downloaded)},
		"left":       []string{strconv.Itoa(p.Left)},
	}

	parsed.RawQuery = v.Encode()

	return parsed.String(), nil
}

func requestPeers(trackerURL string) ([]PeerInfo, error) {
	resp, err := http.Get(trackerURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res HTTPTrackerResponse
	if err := bencode.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	if res.FailureReason != "" {
		return nil, fmt.Errorf("failed to get the peers from HTTP tracker=%s, error=%s", trackerURL, res.FailureReason)
	}

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

func parsePeersBinary(peers []byte) ([]PeerInfo, error) {
	if len(peers)%6 != 0 {
		return nil, fmt.Errorf("peers received in wrong format: not divisible by 6 - %d", len(peers))
	}

	parsed := make([]PeerInfo, len(peers)/6)

	for i := range parsed {
		b := [6]byte{}
		copy(b[:], peers[i:i+6])

		parsed[i] = PeerInfo{
			IP:   fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3]),
			Port: binary.BigEndian.Uint16([]byte{b[4], b[5]}),
		}
	}

	return parsed, nil
}
