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
	peerCh         <-chan PeerAddress
}

func New(trackerURL string, infoHash [20]byte, peerCh <-chan PeerAddress) (*tracker, error) {
	parsed, err := url.Parse(trackerURL)
	if err != nil {
		return nil, err
	}

	t := &tracker{url: parsed, infoHash: infoHash}
	t.classifyTracker()

	return t, nil
}

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
