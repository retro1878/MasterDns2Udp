// ==============================================================================
// MasterDns2Udp

// Github: https://github.com/retro1878/MasterDns2Udp
// Year: 2026
// ==============================================================================

package viotcp

import (
	"encoding/binary"
	"math/rand"
)

// Violated TCP packet parameters — base values kept for non-randomised fields.
const (
	vioTTL      = 64
	vioWindow   = 8192
	vioTCPFlags = 0x18 // PSH + ACK
	vioIPFlags  = 0    // no DF, no MF

	ipHeaderLen  = 20
	tcpHeaderLen = 32 // 20 base + 12 bytes of TCP options
	tcpDataOff   = tcpHeaderLen / 4 // 8
)

// tcpOptions is the fixed 12-byte TCP options block.
//
//	MSS=1280   (02 04 05 00)
//	WScale=8   (03 03 08)
//	SackOK     (04 02)
//	EOL×3      (00 00 00)
var tcpOptions = [12]byte{
	0x02, 0x04, 0x05, 0x00,
	0x03, 0x03, 0x08,
	0x04, 0x02,
	0x00, 0x00, 0x00,
}

// BuildPacket assembles a complete violated TCP packet.
// seq, ack, and ipID are randomised per call by the Sender; passing explicit
// values allows testing and the hole-puncher to control them.
func BuildPacket(srcIP, dstIP []byte, srcPort, dstPort uint16, seq, ack uint32, ipID uint16, payload []byte) []byte {
	totalLen := ipHeaderLen + tcpHeaderLen + len(payload)
	pkt := make([]byte, totalLen)

	// ── IP Header (20 bytes) ──────────────────────────────────────────────────
	pkt[0] = 0x45
	pkt[1] = 0x00
	binary.BigEndian.PutUint16(pkt[2:4], uint16(totalLen))
	binary.BigEndian.PutUint16(pkt[4:6], ipID)
	binary.BigEndian.PutUint16(pkt[6:8], vioIPFlags)
	pkt[8] = vioTTL
	pkt[9] = 0x06 // TCP
	copy(pkt[12:16], srcIP)
	copy(pkt[16:20], dstIP)
	binary.BigEndian.PutUint16(pkt[10:12], internetChecksum(pkt[:ipHeaderLen]))

	// ── TCP Header (32 bytes: 20 base + 12 options) ───────────────────────────
	tcp := pkt[ipHeaderLen:]
	binary.BigEndian.PutUint16(tcp[0:2], srcPort)
	binary.BigEndian.PutUint16(tcp[2:4], dstPort)
	binary.BigEndian.PutUint32(tcp[4:8], seq)
	binary.BigEndian.PutUint32(tcp[8:12], ack)
	tcp[12] = tcpDataOff << 4
	tcp[13] = vioTCPFlags
	binary.BigEndian.PutUint16(tcp[14:16], vioWindow)
	copy(tcp[20:32], tcpOptions[:])
	copy(tcp[32:], payload)

	tcpSegLen := uint16(tcpHeaderLen + len(payload))
	binary.BigEndian.PutUint16(tcp[16:18], tcpPseudoChecksum(srcIP, dstIP, tcpSegLen, tcp))

	return pkt
}

// randU32 returns a random uint32 in [low, math.MaxUint32].
func randU32() uint32 {
	return rand.Uint32()
}

// randU16 returns a random uint16 > 0.
func randU16() uint16 {
	v := uint16(rand.Uint32())
	if v == 0 {
		v = 1
	}
	return v
}

// internetChecksum computes the RFC 1071 one's-complement sum used by IP and TCP.
func internetChecksum(data []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(data); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
	}
	if len(data)%2 != 0 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// tcpPseudoChecksum builds a TCP pseudo-header and checksums pseudo + segment.
func tcpPseudoChecksum(srcIP, dstIP []byte, tcpLen uint16, tcpSeg []byte) uint16 {
	pseudo := make([]byte, 12+len(tcpSeg))
	copy(pseudo[0:4], srcIP)
	copy(pseudo[4:8], dstIP)
	pseudo[8] = 0x00
	pseudo[9] = 0x06 // TCP
	binary.BigEndian.PutUint16(pseudo[10:12], tcpLen)
	copy(pseudo[12:], tcpSeg)
	return internetChecksum(pseudo)
}
