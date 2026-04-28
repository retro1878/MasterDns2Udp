// ==============================================================================
// MasterDns2Udp

// Github: https://github.com/retro1878/MasterDns2Udp
// Year: 2026
// ==============================================================================

package viotcp

import (
	"encoding/binary"
)

// Violated TCP packet signature — mirrors GFW-Knocker's proven evasion fingerprint.
// Reference: github.com/GFW-knocker/gfw_resist_tcp_proxy
const (
	vioTTL      = 64
	vioWindow   = 8192
	vioSeq      = 1  // static server-side sequence number
	vioAck      = 0  // no acknowledgement
	vioTCPFlags = 0x18 // PSH + ACK
	vioIPID     = 1  // static IP identification — part of the violated signature
	vioIPFlags  = 0  // no DF, no MF (IP flags = 0 = "violated")

	ipHeaderLen  = 20
	tcpHeaderLen = 32 // 20 base + 12 bytes of TCP options
	tcpDataOff   = tcpHeaderLen / 4 // 8
)

// tcpOptions is the fixed 12-byte TCP options block that makes the packet look
// like a connection-establishment segment injected mid-stream:
//
//	MSS=1280   (02 04 05 00)
//	WScale=8   (03 03 08)
//	SackOK     (04 02)
//	EOL×3      (00 00 00)  padding to 4-byte boundary
var tcpOptions = [12]byte{
	0x02, 0x04, 0x05, 0x00, // MSS = 1280
	0x03, 0x03, 0x08,       // WScale = 8
	0x04, 0x02,             // SackOK
	0x00, 0x00, 0x00,       // EOL padding
}

// BuildPacket assembles a complete violated TCP packet (IP header + TCP header + payload).
// The caller must provide all four-byte IPv4 addresses.
func BuildPacket(srcIP, dstIP []byte, srcPort, dstPort uint16, payload []byte) []byte {
	totalLen := ipHeaderLen + tcpHeaderLen + len(payload)
	pkt := make([]byte, totalLen)

	// ── IP Header (20 bytes) ──────────────────────────────────────────────────
	pkt[0] = 0x45                                                // Version=4, IHL=5 (20 bytes)
	pkt[1] = 0x00                                                // DSCP/ECN
	binary.BigEndian.PutUint16(pkt[2:4], uint16(totalLen))       // Total Length
	binary.BigEndian.PutUint16(pkt[4:6], vioIPID)                // IP ID = 1
	binary.BigEndian.PutUint16(pkt[6:8], vioIPFlags)             // Flags=0, FragOffset=0
	pkt[8] = vioTTL                                              // TTL = 64
	pkt[9] = 0x06                                                // Protocol = TCP
	// pkt[10:12] IP checksum filled below
	copy(pkt[12:16], srcIP)
	copy(pkt[16:20], dstIP)
	binary.BigEndian.PutUint16(pkt[10:12], internetChecksum(pkt[:ipHeaderLen]))

	// ── TCP Header (32 bytes: 20 base + 12 options) ───────────────────────────
	tcp := pkt[ipHeaderLen:]
	binary.BigEndian.PutUint16(tcp[0:2], srcPort)
	binary.BigEndian.PutUint16(tcp[2:4], dstPort)
	binary.BigEndian.PutUint32(tcp[4:8], vioSeq)                 // Seq = 1
	binary.BigEndian.PutUint32(tcp[8:12], vioAck)                // Ack = 0
	tcp[12] = tcpDataOff << 4                                    // Data Offset = 8 (32 bytes header)
	tcp[13] = vioTCPFlags                                        // PSH + ACK
	binary.BigEndian.PutUint16(tcp[14:16], vioWindow)            // Window = 8192
	// tcp[16:18] TCP checksum filled below
	// tcp[18:20] Urgent Pointer = 0
	copy(tcp[20:32], tcpOptions[:])
	copy(tcp[32:], payload)

	tcpSegLen := uint16(tcpHeaderLen + len(payload))
	binary.BigEndian.PutUint16(tcp[16:18], tcpPseudoChecksum(srcIP, dstIP, tcpSegLen, tcp))

	return pkt
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
