//go:build darwin || freebsd || netbsd || openbsd

package main

import (
	"syscall"
	"unsafe"
)

// isTerminal reports whether fd is a terminal. A termios query succeeds
// only on terminals, unlike ModeCharDevice, which /dev/null also has.
func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGETA, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
