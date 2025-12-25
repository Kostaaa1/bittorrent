package torrent

type Peer struct {
	PeerID *string `bencode:"peer id"`
	IP     string  `bencode:"ip"`
	Port   uint16  `bencode:"port"`
	Choked bool
}

type TrackerResponse struct {
	FailureReason  string `bencode:"failure reason"`
	WarningMessage string `bencode:"warning reason"`
	Interval       int    `bencode:"interval"`
	MinInterval    int    `bencode:"min interval"`
	TrackerID      string `bencode:"tracker id"`
	Complete       int    `bencode:"complete"`
	Incomplete     int    `bencode:"incomplete"`
	Peers          []Peer `bencode:"peers"`
	Peers6         string `bencode:"peers6"`
	// Peers  []byte `bencode:"peers"`
}
