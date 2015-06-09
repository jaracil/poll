#poll [![GoDoc](https://godoc.org/github.com/jaracil/poll?status.png)](https://godoc.org/github.com/jaracil/poll)
poll is an efficient char device access package for Go

Download:
```shell
go get github.com/jaracil/poll
```

##Description:

Poll is an efficient char device access package for Go, based on Nick Patavalis
(npat@efault.net) poller package.

It uses EPOLL(7) on Linux and SELECT(2) on the rest of Posix Oses.
Allows concurent Read and Write operations from and to multiple
file-descriptors without allocating one OS thread for every blocked
operation. It behaves similarly to Go's netpoller (which multiplexes
network connections) without requiring special support from the Go
runtime.

It can be used with tty devices, character devices, pipes,
FIFOs, GPIOs, TUNs/TAPs and any Unix file-descriptor that is epoll(7)-able
or select(2)-able. In addition it allows the user to set timeouts (deadlines)
for read and write operations.

All operations on *poll.File are thread-safe; you can use the same File
from multiple go-routines. It is, for example, safe to close a file
blocked on a Read or Write call from another go-routine. In this case all blocked read/write operations are awakened.
* * *

