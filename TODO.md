- BUG: Peer can choke forever
- BUG: If peer has 0 assigned pieces, ask scheduler to reassign it to you. If unnassigned map is empty, then take some pieces from another peer.
- BUG: If peer chokes in the middle of sending pieces. instead of requesting the piece from the beginning, need to continue requesting,
  Need to track which blocks are downloaded in writer.go. currently blockCount is being used, which is bad.

- TODO:
- Improve data structures, a lot can be simplified
- Give peer a time window to respond to request. If request is sent and if the peer does not respond with pieces
