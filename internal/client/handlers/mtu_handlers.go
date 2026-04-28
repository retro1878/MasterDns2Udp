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
	RegisterHandler(Enums.PACKET_MTU_UP_RES, handleMTUResponse)
	RegisterHandler(Enums.PACKET_MTU_DOWN_RES, handleMTUResponse)
}

func handleMTUResponse(c ClientContext, packet VpnProto.Packet, addr *net.UDPAddr) error {
	return c.HandleMTUResponse(packet)
}
