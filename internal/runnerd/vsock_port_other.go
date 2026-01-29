//go:build !darwin

package runnerd

import "errors"

func pickFreeVsockPort() (int, error) {
	return 0, errors.New("vsock is only supported on darwin hosts")
}

