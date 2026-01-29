package tracker

import (
	"fmt"
	"net/url"
)

type tracker struct {
	url            *url.URL
	infoHash       [20]byte
	classification string
	isSecure       bool
	peerCh         <-chan peerInfo
}

func New(trackerURL string, infoHash [20]byte, peerCh <-chan peerInfo) (*tracker, error) {
	parsed, err := url.Parse(trackerURL)
	if err != nil {
		return nil, err
	}

	t := &tracker{
		url:      parsed,
		infoHash: infoHash,
	}

	t.classifyTracker()

	return t, nil
}

// func (t *tracker) Announce(peerID [20]byte, port uint16) error {
// 	params := HTTPTrackerRequestParams{
// 		Tracker:  t.url.String(),
// 		InfoHash: t.infoHash,
// 		PeerID:   peerID,
// 		Port:     port,
// 		Uploaded:   0,
// 		Downloaded: 0,
// 		Left:       0,
// 		Compact:  0,
// 		NoPeerID: 1,
// 	}

// 	u, err := buildHTTPTrackerURL(params)
// 	if err != nil {
// 		return err
// 	}

// 	http.Get(u)

// }

func (t *tracker) classifyTracker() error {
	switch t.url.Scheme {
	case "http":
		t.classification = "HTTP Tracker"
	case "https":
		t.isSecure = true
		t.classification = "HTTPS Tracker (secure)"
	case "udp":
		t.classification = "UDP Tracker"
	case "ws":
		t.classification = "Websocket Tracker"
	case "wss":
		t.isSecure = true
		t.classification = "Secure Websocket Tracker"
	}

	// if strings.Contains(tr.url.String(), "announce?passkey=") {
	// 	tr.classification = "Private Tracker (with passkey)"
	// }

	if t.classification == "" {
		return fmt.Errorf("unknown tracker type: tracker=%s", t.url.String())
	}

	return nil
}
