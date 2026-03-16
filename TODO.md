-- Do i really need to check for requested blocks???

1.  [DONE] BUG: if peer choke us, no way of unassigning pieces to scheduler
    Solution:
    1. when choke happens, give peer a deadline to unchoke us. if it exceeds, then unassign all pieces to scheduler.

2.  BUG: if peer does not respond with PIECE message to REQUEST message, it will cause deadlock with assigned pieces. Need to reassign pieces back to scehduler if we get no responses for some time.
    Solution:
    Give each REQUEST a deadline. if PIECE response does not happen within this deadline, unassign piece to scheduler. If this happens a lot, unassign all pieces

3.  'End Game Mode'
    When a download is almost complete, there's a tendency for the last few pieces to all be downloaded off a single hosed modem line, taking a very long time. To make sure the last few pieces come in quickly, once requests for all pieces a given downloader doesn't have yet are currently pending, it sends requests for everything to everyone it's downloading from. To keep this from becoming horribly inefficient, it sends cancels to everyone else every time a piece arrives.

4.  Look into building a function for reassigning pieces from other peers when unassigned is empty. Pieces are being scheduled to peers when they have 0 assigned pieces, or when PIECE gets downloaded, then it will check if peer can accept new pieces.
