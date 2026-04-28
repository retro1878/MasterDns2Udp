// ==============================================================================
// MasterDns2Udp

// Github: https://github.com/retro1878/MasterDns2Udp
// Year: 2026
// ==============================================================================

package udpserver

import (
	"context"
	"net"
	"time"

	Enums "masterdns2udp/internal/enums"
	VpnProto "masterdns2udp/internal/vpnproto"
)

// runRawUDPUploadReader reads raw encrypted VPN packets from the dedicated UDP
// upload port. These arrive from clients that send via SOCKS5 UDP ASSOCIATE paths
// instead of (or in addition to) the normal DNS upload channel.
func (s *Server) runRawUDPUploadReader(ctx context.Context, conn *net.UDPConn) {
	buf := make([]byte, 65535)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				return
			}
		}
		if n == 0 {
			continue
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		s.handleRawUploadPacket(pkt)
	}
}

// handleRawUploadPacket processes a single raw encrypted VPN packet received on
// the UDP upload channel. It bypasses DNS parsing and feeds directly into the
// existing post-session pipeline; ARQ deduplication handles duplicate packets
// that arrive via both the DNS path and SOCKS5 path simultaneously.
func (s *Server) handleRawUploadPacket(data []byte) {
	decrypted, err := s.codec.Decrypt(data)
	if err != nil {
		return
	}

	vpnPacket, err := VpnProto.ParseInflated(decrypted)
	if err != nil {
		return
	}

	// Pre-session packets (MTU probe, session init, etc.) are not valid over the
	// raw upload path — those must go through the DNS channel.
	if isPreSessionRequestType(vpnPacket.PacketType) {
		return
	}

	if vpnPacket.PacketType == Enums.PACKET_SESSION_CLOSE {
		s.handleSessionCloseNotice(vpnPacket, time.Now())
		return
	}

	validation := s.sessions.ValidateAndTouch(vpnPacket.SessionID, vpnPacket.SessionCookie, time.Now())
	if !validation.Valid {
		return
	}

	if !s.handlePostSessionPacket(vpnPacket, validation.Active) {
		return
	}

	s.signalUDPSend()
}
