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

// Sender is a no-op stub on non-Linux platforms.
type Sender struct{}

func NewSender(_ net.IP, _ uint16) (*Sender, error) {
	return nil, fmt.Errorf("viotcp: raw socket sender not supported on this platform")
}

func (s *Sender) Send(_ net.IP, _ uint16, _ []byte) error {
	return fmt.Errorf("viotcp: not supported")
}

func (s *Sender) Close() error { return nil }
