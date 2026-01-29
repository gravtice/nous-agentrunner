//go:build !darwin

package runnerd

func isVsockUnavailable(error) bool { return false }

