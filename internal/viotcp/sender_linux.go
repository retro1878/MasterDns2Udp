//go:build linux

// ==============================================================================
// MasterDns2Udp

// Github: https://github.com/retro1878/MasterDns2Udp
// Year: 2026
// ==============================================================================

package viotcp

import (
	"fmt"
	"net"
	"sync"
	"syscall"
)

// Sender transmits violated TCP packets via a raw IP socket (IPPROTO_RAW +
// IP_HDRINCL). Requires CAP_NET_RAW / root. Thread-safe.
type Sender struct {
	fd      int
	srcIP   []byte // 4-byte IPv4
	srcPort uint16
	mu      sync.Mutex
}

// NewSender opens a raw IP socket and returns a Sender bound to the given
// source IP and source TCP port.
func NewSender(srcIP net.IP, srcPort uint16) (*Sender, error) {
	ip4 := srcIP.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("viotcp sender: srcIP must be IPv4")
	}
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_RAW)
	if err != nil {
		return nil, fmt.Errorf("viotcp sender: socket: %w", err)
	}
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, syscall.IP_HDRINCL, 1); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("viotcp sender: IP_HDRINCL: %w", err)
	}
	return &Sender{fd: fd, srcIP: ip4, srcPort: srcPort}, nil
}

// Send crafts a violated TCP packet carrying payload and delivers it to
// dstIP:dstPort. Non-blocking after the kernel accepts the packet.
func (s *Sender) Send(dstIP net.IP, dstPort uint16, payload []byte) error {
	dst4 := dstIP.To4()
	if dst4 == nil {
		return nil // ignore non-IPv4 destinations silently
	}
	pkt := BuildPacket(s.srcIP, dst4, s.srcPort, dstPort, payload)

	var addr syscall.SockaddrInet4
	copy(addr.Addr[:], dst4)

	s.mu.Lock()
	err := syscall.Sendto(s.fd, pkt, 0, &addr)
	s.mu.Unlock()
	return err
}

// Close releases the underlying raw socket.
func (s *Sender) Close() error {
	return syscall.Close(s.fd)
}
