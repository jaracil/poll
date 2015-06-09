// +build select darwin freebsd dragonfly netbsd openbsd plan9 solaris

// Copyright (c) 2015, Jose Luis Aracil Gomez (pepe@diselpro.com)
// Based on Nick Patavalis (npat@efault.net) poller package.
// All rights reserved.
// Use of this source code is governed by a BSD-style license that can
// be found in the LICENSE.txt file.

package poll

import (
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var fdTrR map[int]struct{} = map[int]struct{}{}
var fdTrW map[int]struct{} = map[int]struct{}{}
var fdTrLock sync.Mutex

var fdm map[int]*File = map[int]*File{}
var fdmLock sync.Mutex
var wakeupR *os.File
var wakeupW *os.File

func init() {
	var err error
	wakeupR, wakeupW, err = os.Pipe()
	if err != nil {
		panic(err.Error())
	}
	err = syscall.SetNonblock(int(wakeupR.Fd()), true)
	if err != nil {
		panic(err.Error())
	}
	err = syscall.SetNonblock(int(wakeupW.Fd()), true)
	if err != nil {
		panic(err.Error())
	}
	go evLoop()
}

func wakeup() {
	wakeupW.Write([]byte{0})
}

func startTrack(fd int, write bool) {
	fdTrLock.Lock()
	last := false
	if write {
		_, last = fdTrW[fd]
		fdTrW[fd] = struct{}{}
	} else {
		_, last = fdTrR[fd]
		fdTrR[fd] = struct{}{}
	}
	if !last {
		wakeup()
	}
	fdTrLock.Unlock()
}

func stopTrack(fd int, write bool) {
	fdTrLock.Lock()
	last := false
	if write {
		_, last = fdTrW[fd]
		delete(fdTrW, fd)
	} else {
		_, last = fdTrR[fd]
		delete(fdTrR, fd)
	}
	if last {
		wakeup()
	}
	fdTrLock.Unlock()
}

func register(f *File) error {
	fdmLock.Lock()
	fdm[f.fd] = f
	fdmLock.Unlock()
	return nil
}

func unregister(f *File) error {
	fdmLock.Lock()
	delete(fdm, f.fd)
	fdmLock.Unlock()
	return nil
}

func getFile(fd int) *File {
	fdmLock.Lock()
	f := fdm[fd]
	fdmLock.Unlock()
	return f
}

func evLoop() {
	dummy := make([]byte, 1024)
	fdR := &FdSet{}
	fdW := &FdSet{}
	wupFd := int(wakeupR.Fd())
	topFd := wupFd
	for {
		topFd = wupFd
		fdR.Reset()
		fdW.Reset()
		fdR.Set(wupFd)
		fdTrLock.Lock()
		for k, _ := range fdTrR {
			fdR.Set(k)
			if k > topFd {
				topFd = k
			}
		}
		for k, _ := range fdTrW {
			fdW.Set(k)
			if k > topFd {
				topFd = k
			}
		}
		fdTrLock.Unlock()
		n, err := Select(topFd+1, fdR, fdW, nil, -1)
		if err != nil {
			continue
		}
		if fdR.IsSet(wupFd) {
			n--
			wakeupR.Read(dummy)
		}
		for x := 0; x <= topFd && n > 0; x++ {
			if fdR.IsSet(x) {
				n--
				fdTrLock.Lock()
				delete(fdTrR, x)
				fdTrLock.Unlock()
				file := getFile(x)
				if file != nil {
					file.r.cond.L.Lock()
					file.r.cond.Broadcast()
					file.r.cond.L.Unlock()
				}
			}
			if fdW.IsSet(x) {
				n--
				fdTrLock.Lock()
				delete(fdTrW, x)
				fdTrLock.Unlock()
				file := getFile(x)
				if file != nil {
					file.w.cond.L.Lock()
					file.w.cond.Broadcast()
					file.w.cond.L.Unlock()
				}
			}
		}
	}
}

// Select syscall stuff

const (
	FD_BITS    = int(unsafe.Sizeof(0) * 8)
	FD_SETSIZE = 1024
)

type FdSet struct {
	bits [FD_SETSIZE / FD_BITS]int
}

func (fds *FdSet) Reset() {
	for i := 0; i < len(fds.bits); i++ {
		fds.bits[i] = 0
	}
}

func (fds *FdSet) Set(fd int) {
	mask := uint(1) << (uint(fd) % uint(FD_BITS))

	fds.bits[fd/FD_BITS] |= int(mask)
}

func (fds *FdSet) UnSet(fd int) {
	mask := uint(1) << (uint(fd) % uint(FD_BITS))

	fds.bits[fd/FD_BITS] &^= int(mask)
}

func (fds *FdSet) IsSet(fd int) bool {
	mask := uint(1) << (uint(fd) % uint(FD_BITS))

	return (fds.bits[fd/FD_BITS] & int(mask)) != 0
}

func Select(n int, r, w, e *FdSet, timeout time.Duration) (int, error) {
	rfds := (*syscall.FdSet)(unsafe.Pointer(r))
	wfds := (*syscall.FdSet)(unsafe.Pointer(w))
	efds := (*syscall.FdSet)(unsafe.Pointer(e))

	if timeout >= 0 {
		tv := syscall.NsecToTimeval(timeout.Nanoseconds())
		return syscall.Select(n, rfds, wfds, efds, &tv)
	}
	return syscall.Select(n, rfds, wfds, efds, nil)
}
