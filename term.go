package main

import (
	"syscall"
	"unsafe"
)

func termios(fd, req uintptr, t *syscall.Termios) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(t))); errno != 0 {
		return errno
	}
	return nil
}

// isTerminal reports whether fd is a terminal. A termios query succeeds
// only on terminals, unlike ModeCharDevice, which /dev/null also has.
func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	return termios(fd, ioctlGetTermios, &t) == nil
}

// termSize returns fd's width and height, or 80x24 if unknown.
func termSize(fd uintptr) (width, height int) {
	var ws struct{ Row, Col, X, Y uint16 }
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); errno != 0 || ws.Col == 0 || ws.Row == 0 {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}

// makeRaw turns off line buffering, echo and signal keys on fd so each
// key press, including Ctrl-C, arrives as bytes. Output processing stays
// on, so the printer's newlines still return the cursor.
func makeRaw(fd uintptr) (restore func(), err error) {
	var old syscall.Termios
	if err := termios(fd, ioctlGetTermios, &old); err != nil {
		return nil, err
	}
	t := old
	t.Lflag &^= syscall.ICANON | syscall.ECHO | syscall.ISIG
	t.Cc[syscall.VMIN], t.Cc[syscall.VTIME] = 1, 0
	if err := termios(fd, ioctlSetTermios, &t); err != nil {
		return nil, err
	}
	return func() { termios(fd, ioctlSetTermios, &old) }, nil
}
