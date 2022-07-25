//go:build linux && !select
// +build linux,!select

// Copyright (c) 2015, Jose Luis Aracil Gomez (pepe@diselpro.com)
// Based on Nick Patavalis (npat@efault.net) poller package.
// All rights reserved.
// Use of this source code is governed by a BSD-style license that can
// be found in the LICENSE.txt file.

package poll

import (
	"log"
	"sync"
	"syscall"
)

var epfd int = -1
var fdm map[int]*File = map[int]*File{}
var fdmLock sync.Mutex

func init() {
	fd, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		log.Panicf("poller: EpollCreate1: %s", err.Error())
	}
	epfd = fd
	go evLoop()
}

func startTrack(fd int, write bool) {} // startTRack non needed in epoll loop
func stopTrack(fd int, write bool)  {} // stopTRack non needed in epoll loop

func register(f *File) (err error) {
	fdmLock.Lock()
	fdm[f.fd] = f
	ev := syscall.EpollEvent{
		Events: syscall.EPOLLIN |
			syscall.EPOLLOUT |
			syscall.EPOLLRDHUP |
			(syscall.EPOLLET & 0xffffffff),
		Fd: int32(f.fd)}
	err = syscall.EpollCtl(epfd, syscall.EPOLL_CTL_ADD, f.fd, &ev)
	fdmLock.Unlock()
	return
}

func unregister(f *File) (err error) {
	fdmLock.Lock()
	delete(fdm, f.fd)
	var ev syscall.EpollEvent
	err = syscall.EpollCtl(epfd, syscall.EPOLL_CTL_DEL, f.fd, &ev)
	fdmLock.Unlock()
	return
}

func epollEv(ev *syscall.EpollEvent, write bool) {
	var fdc *fdCtl
	fdmLock.Lock()
	fd := fdm[int(ev.Fd)]
	fdmLock.Unlock()
	if fd == nil {
		// Drop event. Probably stale FD.
		return
	}
	if !write {
		fdc = &fd.r
	} else {
		fdc = &fd.w
	}
	fdc.cond.L.Lock()
	fdc.cond.Broadcast()
	fdc.cond.L.Unlock()
}

func evLoop() {
	events := make([]syscall.EpollEvent, 128)
	for {
		n, err := syscall.EpollWait(epfd, events, -1)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			log.Panicf("poller: EpollWait: %s", err.Error())
		}
		for i := 0; i < n; i++ {
			ev := &events[i]
			if ev.Events&(syscall.EPOLLIN|
				syscall.EPOLLRDHUP|
				syscall.EPOLLHUP|
				syscall.EPOLLERR) != 0 {
				epollEv(ev, false)
			}
			if ev.Events&(syscall.EPOLLOUT|
				syscall.EPOLLHUP|
				syscall.EPOLLERR) != 0 {
				epollEv(ev, true)
			}
		}
	}
}
