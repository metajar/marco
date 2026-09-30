//go:build !linux && !darwin

package probe

import "errors"

var errUnsupported = errors.New("neighbor table not supported on this platform")

func readNeighbors() ([]Neighbor, error) { return nil, errUnsupported }

func flushNeighbor(string) error { return errUnsupported }
