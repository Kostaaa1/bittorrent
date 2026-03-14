1. [DONE] BUG: if peer choke us, no way of unassigning pieces to scheduler
   Solution:
   1. when choke happens, give peer a deadline to unchoke us. if it exceeds, then unassign all pieces to scheduler.

2. BUG: if peer does not respond with PIECE message to REQUEST message, it will cause deadlock with assigned pieces. Need to reassign pieces back to scehduler if we get no responses for some time.
   Solution:
   1. yoo
   2. yoo
