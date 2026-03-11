BUG: deadlock on peers that are choking us or not responding to REQUEST requests.

Solution: give timeout, for example if peer do not get unchoked for eg. 1 minute, then reassign all pieces to scheduler or between peers? For requests, if we do not get PIECE response for some period, eg. 1 minute, then unassign that piece to scheduler (delete pending from blocks, and remove from assigned slice). If that happens 2,3 times, unassign all pieces and close the connection.

- If peer has 0 assigned pieces (it will happen for fast peers), enable reassigning from other pieces
