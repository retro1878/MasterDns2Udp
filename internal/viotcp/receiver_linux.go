//go:build linux

// ==============================================================================
// MasterDns2Udp

// Github: https://github.com/retro1878/MasterDns2Udp
// Year: 2026
// ==============================================================================

package viotcp

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
)

// Receiver captures violated TCP packets via a raw TCP socket (IPPROTO_TCP).
// It filters by source IP and source port so only packets from the abroad VPS
// are delivered to the caller. Requires CAP_NET_RAW / root.
//
// Note: on Linux, SOCK_RAW + IPPROTO_TCP delivers packets after netfilter
// INPUT processing. Do NOT add an iptables INPUT DROP rule on the receive port,
// as that would prevent the raw socket from seeing the packets. The kernel's TCP
// stack will emit RST in response to the unsolicited PSH+ACK, but the server
// ignores RST since it uses a raw sender with no TCP state machine.
type Receiver struct {
	fd            int
	filterSrcIP   [4]byte
	filterSrcPort uint16
}

// NewReceiver opens a raw TCP socket and returns a Receiver that accepts only
// packets arriving from filterSrcIP:filterSrcPort.
func NewReceiver(filterSrcIP net.IP, filterSrcPort uint16) (*Receiver, error) {
	ip4 := filterSrcIP.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("viotcp receiver: filterSrcIP must be IPv4")
	}
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_TCP)
	if err != nil {
		return nil, fmt.Errorf("viotcp receiver: socket: %w", err)
	}
	var r Receiver
	r.fd = fd
	copy(r.filterSrcIP[:], ip4)
	r.filterSrcPort = filterSrcPort
	return &r, nil
}

// ReadPayload blocks until a violated TCP packet matching the source filter
// arrives and writes its TCP payload into buf.
// Returns the number of payload bytes written. Returns an error only on fatal
// socket failure; callers should retry on transient errors.
func (r *Receiver) ReadPayload(buf []byte) (int, error) {
	tmp := make([]byte, 65535)
	for {
		n, _, err := syscall.Recvfrom(r.fd, tmp, 0)
		if err != nil {
			return 0, err
		}
		if n < 20 {
			continue
		}

		// IP header: check protocol and source IP.
		ihl := int(tmp[0]&0x0f) * 4
		if n < ihl+20 {
			continue
		}
		if tmp[9] != 0x06 { // not TCP
			continue
		}
		if tmp[12] != r.filterSrcIP[0] || tmp[13] != r.filterSrcIP[1] ||
			tmp[14] != r.filterSrcIP[2] || tmp[15] != r.filterSrcIP[3] {
			continue
		}

		// TCP header: check source port.
		tcpSrcPort := binary.BigEndian.Uint16(tmp[ihl : ihl+2])
		if tcpSrcPort != r.filterSrcPort {
			continue
		}

		// Locate the TCP payload.
		dataOffset := int(tmp[ihl+12]>>4) * 4
		payloadStart := ihl + dataOffset
		if payloadStart >= n {
			continue // empty segment (ACK, etc.)
		}

		payloadLen := n - payloadStart
		if payloadLen > len(buf) {
			payloadLen = len(buf)
		}
		copy(buf[:payloadLen], tmp[payloadStart:payloadStart+payloadLen])
		return payloadLen, nil
	}
}

// Close releases the underlying raw socket.
func (r *Receiver) Close() error {
	return syscall.Close(r.fd)
}
