//go:build darwin

package runnerd

import (
	"errors"

	"golang.org/x/sys/unix"
)

func isVsockUnavailable(err error) bool {
	return errors.Is(err, unix.EAFNOSUPPORT) ||
		errors.Is(err, unix.EPROTONOSUPPORT) ||
		errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.ENOTSUP) ||
		errors.Is(err, unix.ENODEV)
}

