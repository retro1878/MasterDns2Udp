// ==============================================================================
// MasterDns2Udp

// Github: https://github.com/retro1878/MasterDns2Udp
// Year: 2026
// ==============================================================================

package udpserver

import (
	"errors"
	"fmt"
	"time"

	DnsParser "masterdns2udp/internal/dnsparser"
	domainMatcher "masterdns2udp/internal/domainmatcher"
	Enums "masterdns2udp/internal/enums"
	VpnProto "masterdns2udp/internal/vpnproto"
)

func (s *Server) handlePacket(packet []byte) []byte {
	parsed, err := DnsParser.ParseDNSRequestLite(packet)
	if err != nil {
		if errors.Is(err, DnsParser.ErrNotDNSRequest) || errors.Is(err, DnsParser.ErrPacketTooShort) {
			return nil
		}

		return s.buildNoDataResponseLogged(packet, "request-parse-failed")
	}

	if !parsed.HasQuestion {
		return s.buildNoDataResponseLogged(packet, "request-has-no-question")
	}

	decision := s.domainMatcher.Match(parsed)
	if decision.Action == domainMatcher.ActionProcess {
		response := s.handleTunnelCandidate(packet, parsed, decision)
		if response != nil {
			return response
		}

		return s.buildNoDataResponseLiteLogged(packet, parsed, "domain-match-process-failed")
	}

	if decision.Action == domainMatcher.ActionFormatError || decision.Action == domainMatcher.ActionNoData {
		return s.buildNoDataResponseLiteLogged(packet, parsed, "domain-match-no-data")
	}

	return s.buildNoDataResponseLiteLogged(packet, parsed, "domain-match-no-data")
}

func (s *Server) handleTunnelCandidate(packet []byte, parsed DnsParser.LitePacket, decision domainMatcher.Decision) []byte {
	vpnPacket, err := VpnProto.ParseInflatedFromLabels(decision.Labels, s.codec)
	if err != nil {
		return s.buildNoDataResponseLiteLogged(packet, parsed, "vpn-proto-parse-failed")
	}

	if vpnPacket.PacketType == Enums.PACKET_SESSION_CLOSE {
		s.handleSessionCloseNotice(vpnPacket, time.Now())
		return s.buildNoDataResponseLiteLogged(packet, parsed, "session-close-notice")
	}

	if !isPreSessionRequestType(vpnPacket.PacketType) {
		validation := s.validatePostSessionPacket(packet, decision.RequestName, vpnPacket)
		if !validation.ok {
			return validation.response
		}

		if !s.handlePostSessionPacket(vpnPacket, validation.record) {
			return s.buildNoDataResponseLiteLogged(packet, parsed, fmt.Sprintf("post-session-unhandled-%s", Enums.PacketTypeName(vpnPacket.PacketType)))
		}

		// All server→client traffic goes via the UDP download channel.
		// Signal the sender and return an empty NOERROR DNS response.
		s.signalUDPSend()
		return s.buildNoDataResponseLiteLogged(packet, parsed, "udp-mode")
	}

	switch vpnPacket.PacketType {
	case Enums.PACKET_MTU_UP_REQ:
		return s.handleMTUUpRequest(packet, parsed, decision, vpnPacket)
	case Enums.PACKET_MTU_DOWN_REQ:
		return s.handleMTUDownRequest(packet, parsed, decision, vpnPacket)
	case Enums.PACKET_SESSION_INIT:
		return s.handleSessionInitRequest(packet, decision, vpnPacket)
	default:
		return s.buildNoDataResponseLiteLogged(packet, parsed, fmt.Sprintf("pre-session-unhandled-%s", Enums.PacketTypeName(vpnPacket.PacketType)))
	}
}
