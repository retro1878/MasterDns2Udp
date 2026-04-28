// ==============================================================================
// MasterDns2Udp

// Github: https://github.com/retro1878/MasterDns2Udp
// Year: 2026
// ==============================================================================
package handlers

import (
	"net"

	Enums "masterdns2udp/internal/enums"
	VpnProto "masterdns2udp/internal/vpnproto"
)

func init() {
	RegisterHandler(Enums.PACKET_SESSION_BUSY, handleSessionBusy)
	RegisterHandler(Enums.PACKET_ERROR_DROP, handleErrorDrop)
}

func handleSessionBusy(c ClientContext, packet VpnProto.Packet, addr *net.UDPAddr) error {
	return c.HandleSessionBusy()
}

func handleErrorDrop(c ClientContext, packet VpnProto.Packet, addr *net.UDPAddr) error {
	return c.HandleErrorDrop(packet)
}
