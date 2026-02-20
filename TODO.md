[] - 1: announcer.go - enable parallel peer discovery for multiple trackers with semaphore

[] - 2: client.go - switch data structure for unassigned/assigned - it can be done with slices

[] - 3: pipeline.go - problem with pieces. pieces is using channel for assigning next active piece (used for requesting). this is a problem because there is no way of getting those request pieces back to reassign to other peers (problem when peer does not send the pieces when requested).

[] - 4: pipeline.go - separate package, provide clean api for assigningPieces, getting nextBlocks, mutating curr

[] - 5: peer.go/writer.go - repeating torrent meta info (pieceLength, blockSize, etc.) calculating same stuff for last piece - maybe send via channel to writer.
