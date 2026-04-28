//go:build !linux

// ==============================================================================
// MasterDns2Udp

// Github: https://github.com/retro1878/MasterDns2Udp
// Year: 2026
// ==============================================================================

package viotcp

import (
	"fmt"
	"net"
)

// Receiver is a no-op stub on non-Linux platforms.
type Receiver struct{}

func NewReceiver(_ net.IP, _ uint16) (*Receiver, error) {
	return nil, fmt.Errorf("viotcp: raw socket receiver not supported on this platform")
}

func (r *Receiver) ReadPayload(_ []byte) (int, error) {
	return 0, fmt.Errorf("viotcp: not supported")
}

func (r *Receiver) Close() error { return nil }
