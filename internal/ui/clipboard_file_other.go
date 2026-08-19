//go:build !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package ui

func readOSClipboardFiles() ([]string, error) { return nil, nil }
