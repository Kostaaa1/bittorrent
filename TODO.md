1. [DONE] BUG: Peer chokes us — unassign pieces back to scheduler after a deadline
2. BUG: No PIECE response to REQUEST causes deadlock — unassign pieces back to scheduler after request deadline
3. End game mode — allow multiple peers to request same blocks near completion, send cancels on arrival
4. Piece stealing — reassign pieces from slow peers when unassigned pool is empty
5. Rarest first piece selection
6. On peer disconnect, reassign both pieces and already-fetched blocks to avoid re-downloading completed blocks
   -- currently just pieces are being reassing, meaning that the whole piece need to be requested from beginning
7. Full support for magnet links
8. HTTP tracker request timeout — http.Get has no timeout, can hang indefinitely
9. Send keep-alive messages periodically — peers drop connection after ~2min of inactivity
10. Handle incoming Have messages — update peer bitfield so scheduler knows they have new pieces
11. Validate peer bitfield length against number of pieces
12. Context cancellation and graceful cleanup on download complete or user cancel
