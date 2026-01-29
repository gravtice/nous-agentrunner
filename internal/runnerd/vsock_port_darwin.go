//go:build darwin

package runnerd

import (
	"crypto/rand"
	"fmt"

	"golang.org/x/sys/unix"
)

func pickFreeVsockPort() (int, error) {
	const min = 20000
	const max = 60000

	for range 128 {
		var b [2]byte
		if _, err := rand.Read(b[:]); err != nil {
			break
		}
		port := min + int(uint16(b[0])<<8|uint16(b[1]))%(max-min+1)
		ok, err := tryBindVsockPort(port)
		if err != nil {
			return 0, err
		}
		if ok {
			return port, nil
		}
	}

	for port := min; port <= max; port++ {
		ok, err := tryBindVsockPort(port)
		if err != nil {
			return 0, err
		}
		if ok {
			return port, nil
		}
	}

	return 0, fmt.Errorf("no free vsock port in %d..%d", min, max)
}

func tryBindVsockPort(port int) (bool, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return false, err
	}
	defer unix.Close(fd)

	if port <= 0 || port > 65535 {
		return false, fmt.Errorf("invalid port %d", port)
	}

	const any = ^uint32(0)
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: any, Port: uint32(port)}); err != nil {
		if err == unix.EADDRINUSE {
			return false, nil
		}
		return false, err
	}
	if err := unix.Listen(fd, 1); err != nil {
		return false, err
	}
	return true, nil
}
