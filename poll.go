// Copyright (c) 2015, Jose Luis Aracil Gomez (pepe@diselpro.com)
// Based on Nick Patavalis (npat@efault.net) poller package.
// All rights reserved.
// Use of this source code is governed by a BSD-style license that can
// be found in the LICENSE.txt file.

package poll

import (
	"io"
	"sync"
	"syscall"
	"time"
)

const (
	O_RDONLY   int = syscall.O_RDONLY   // open the file read-only.
	O_WRONLY   int = syscall.O_WRONLY   // open the file write-only.
	O_RDWR     int = syscall.O_RDWR     // open the file read-write.
	O_NONBLOCK int = syscall.O_NONBLOCK // open in non block mode.
)

// fdCtl keeps control fields (locks, timers, etc) for a single
// direction. For every File there is one fdCtl for Read operations and
// another for Write operations.
type fdCtl struct {
	m        sync.Mutex
	cond     *sync.Cond
	deadline time.Time
	timer    *time.Timer
	timeout  bool
}

// OsFile interface with *os.File methods used in NewFromFile
type OsFile interface {
	Close() error
	Fd() uintptr
	Name() string
}

// File is an *os.File like object who adds polling capabilities
type File struct {
	closed bool // Set by Close(), never cleared
	fd     int
	name   string
	closeF func() error
	// Must hold respective lock to access
	r fdCtl // Control fields for Read operations
	w fdCtl // Control fields for Write operations
}

// NewFile returns a new File with the given file descriptor and name.
func NewFile(fd uintptr, name string) (*File, error) {
	err := syscall.SetNonblock(int(fd), true)
	if err != nil {
		return nil, err
	}
	file := &File{fd: int(fd), name: name}
	file.r.cond = sync.NewCond(&sync.Mutex{})
	file.w.cond = sync.NewCond(&sync.Mutex{})
	err = register(file)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// Open the named path for reading, writing or both, depnding on the
// flags argument.
func Open(name string, flags int) (*File, error) {
	fd, err := syscall.Open(name, flags|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0666)
	if err != nil {
		return nil, err
	}
	return NewFile(uintptr(fd), name)
}

// NewFromFile returns a new *poll.File based on the given *os.File.
// You don't need to worry about closing the *os.File, *poll.File already does it.
func NewFromFile(of OsFile) (*File, error) {
	f, err := NewFile(of.Fd(), of.Name())
	if err != nil {
		return nil, err
	}
	f.closeF = of.Close
	return f, nil
}

// Name returns the name of the file as presented to Open.
func (f *File) Name() string {
	return f.name
}

// Fd returns the integer Unix file descriptor referencing the open file.
func (f *File) Fd() uintptr {
	return uintptr(f.fd)
}

// WriteString is like Write, but writes the contents of string s rather than a slice of bytes.
func (f *File) WriteString(s string) (int, error) {
	return f.Write([]byte(s))
}

// Read reads up to len(b) bytes from the File.
// It returns the number of bytes read and an error, if any.
func (f *File) Read(p []byte) (n int, err error) {
	f.r.m.Lock()
	n, err = f.sysrw(false, p)
	f.r.m.Unlock()
	return
}

// Write writes len(b) bytes to the File.
// It returns the number of bytes written and an error, if any.
// Write returns a non-nil error when n != len(b).
func (f *File) Write(p []byte) (n int, err error) {
	f.w.m.Lock()
	for n != len(p) {
		var nn int
		nn, err = f.sysrw(true, p[n:])
		n += nn
		if err != nil {
			break
		}
	}
	f.w.m.Unlock()
	return
}

func (f *File) sysrw(write bool, p []byte) (n int, err error) {
	var fdc *fdCtl
	var rwfun func(int, []byte) (int, error)
	var errEOF error

	if !write {
		// Prepare things for Read.
		fdc = &f.r
		rwfun = syscall.Read
		errEOF = io.EOF
	} else {
		// Prepare things for Write.
		fdc = &f.w
		rwfun = syscall.Write
		errEOF = io.ErrUnexpectedEOF
	}
	// Read & Write are identical
	fdc.cond.L.Lock()
	defer fdc.cond.L.Unlock()
	for {
		if f.closed {
			return 0, ErrClosed
		}
		if fdc.timeout {
			return 0, ErrTimeout
		}
		n, err = rwfun(f.fd, p)
		if err != nil {
			n = 0
			if err != syscall.EAGAIN {
				break
			}
			// EAGAIN
			startTrack(f.fd, write)
			fdc.cond.Wait()
			if f.closed || fdc.timeout {
				stopTrack(f.fd, write)
			}
			continue
		}
		if n == 0 && len(p) != 0 {
			err = errEOF
			break
		}
		break
	}
	return n, err
}

//Close closes the File, rendering it unusable for I/O. It returns an error, if any.
func (f *File) Close() error {
	if err := f.Lock(); err != nil {
		return err
	}
	defer f.Unlock()
	if f.closed {
		return ErrClosed
	}
	f.closed = true
	unregister(f)
	if f.r.timer != nil {
		f.r.timer.Stop()
	}
	if f.w.timer != nil {
		f.w.timer.Stop()
	}
	// Wake up everybody waiting on File.
	f.r.cond.Broadcast()
	f.w.cond.Broadcast()
	if f.closeF != nil {
		return f.closeF()
	}
	return syscall.Close(f.fd)
}

// SetDeadline sets the deadline for Read and write operations on File.
func (f *File) SetDeadline(t time.Time) error {
	if err := f.SetReadDeadline(t); err != nil {
		return err
	}
	return f.SetWriteDeadline(t)
}

// SetReadDeadline sets the deadline for Read operations on File.
func (f *File) SetReadDeadline(t time.Time) error {
	return f.setDeadline(false, t)
}

// SetWriteDeadline sets the deadline for Write operations on File.
func (f *File) SetWriteDeadline(t time.Time) error {
	return f.setDeadline(true, t)
}

func (f *File) setDeadline(write bool, t time.Time) error {
	var fdc *fdCtl

	if !write {
		// Setting read deadline
		fdc = &f.r
	} else {
		// Setting write deadline
		fdc = &f.w
	}
	// R & W deadlines are handled identically
	fdc.cond.L.Lock()
	if f.closed {
		fdc.cond.L.Unlock()
		return ErrClosed
	}
	fdc.deadline = t
	fdc.timeout = false
	if t.IsZero() {
		if fdc.timer != nil {
			fdc.timer.Stop()
		}
	} else {
		d := t.Sub(time.Now())
		if fdc.timer == nil {
			fdc.timer = time.AfterFunc(d,
				func() { f.timerEvent(write) })
		} else {
			fdc.timer.Stop()
			fdc.timer.Reset(d)
		}
	}
	fdc.cond.L.Unlock()
	return nil
}

// Lock locks the file. It must be called before perfoming
// miscellaneous operations (e.g. ioctls) on the underlying system
// file descriptor.
func (f *File) Lock() error {
	f.r.cond.L.Lock()
	f.w.cond.L.Lock()
	if f.closed {
		f.w.cond.L.Unlock()
		f.r.cond.L.Unlock()
		return ErrClosed
	}
	return nil
}

// Unlock unlocks the file.
func (f *File) Unlock() {
	f.w.cond.L.Unlock()
	f.r.cond.L.Unlock()
}

func (f *File) timerEvent(write bool) {
	var fdc *fdCtl

	if !write {
		// A Read timeout
		fdc = &f.r
	} else {
		// A Write timeout
		fdc = &f.w
	}
	fdc.cond.L.Lock()
	if !f.closed && !fdc.timeout &&
		!fdc.deadline.IsZero() && !fdc.deadline.After(time.Now()) {
		fdc.timeout = true
		fdc.cond.Broadcast()
	}
	fdc.cond.L.Unlock()
}
