// ==============================================================================
// MasterDns2Udp

// Github: https://github.com/retro1878/MasterDns2Udp
// Year: 2026
// ==============================================================================

package client

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"masterdns2udp/internal/logger"
)

// socks5UploaderPool manages a set of SOCKS5 UDP ASSOCIATE connections that
// mirror every outbound VPN packet to alternative upload paths (e.g. ArvanCloud
// CDN reverse tunnels). The server deduplicates incoming packets via ARQ
// sequence numbers, so sending on both DNS and SOCKS5 paths concurrently is safe.
type socks5UploaderPool struct {
	uploaders []*socks5Uploader
}

type socks5Uploader struct {
	proxy      string  // e.g. "127.0.0.1:13000"
	serverIP   net.IP  // server's public IPv4 (4 bytes)
	serverPort uint16  // server UDP_UPLOAD_PORT
	log        *logger.Logger

	mu        sync.Mutex
	udpConn   *net.UDPConn
	relayAddr *net.UDPAddr
	tcpConn   net.Conn
}

func newSocks5UploaderPool(proxies []string, serverIP string, serverPort int, log *logger.Logger) *socks5UploaderPool {
	pool := &socks5UploaderPool{}
	ip4 := net.ParseIP(serverIP).To4()
	if ip4 == nil {
		if log != nil {
			log.Warnf("SOCKS5 upload: invalid SERVER_IP %q — pool disabled", serverIP)
		}
		return pool
	}
	for _, proxy := range proxies {
		pool.uploaders = append(pool.uploaders, &socks5Uploader{
			proxy:      proxy,
			serverIP:   ip4,
			serverPort: uint16(serverPort),
			log:        log,
		})
	}
	return pool
}

// Start launches a maintenance goroutine for each uploader. Each goroutine
// establishes a SOCKS5 UDP ASSOCIATE session and reconnects automatically on
// failure. All goroutines exit when ctx is cancelled.
func (p *socks5UploaderPool) Start(ctx context.Context) {
	for _, u := range p.uploaders {
		go u.maintain(ctx)
	}
}

// SendToAll mirrors data to every uploader that currently has an active
// connection. Non-blocking: uploaders with no active connection are skipped.
func (p *socks5UploaderPool) SendToAll(data []byte) {
	for _, u := range p.uploaders {
		u.send(data)
	}
}

func (u *socks5Uploader) setConn(tcp net.Conn, udp *net.UDPConn, relay *net.UDPAddr) {
	u.mu.Lock()
	u.tcpConn = tcp
	u.udpConn = udp
	u.relayAddr = relay
	u.mu.Unlock()
}

func (u *socks5Uploader) clearConn() {
	u.mu.Lock()
	if u.udpConn != nil {
		_ = u.udpConn.Close()
		u.udpConn = nil
	}
	if u.tcpConn != nil {
		_ = u.tcpConn.Close()
		u.tcpConn = nil
	}
	u.relayAddr = nil
	u.mu.Unlock()
}

// send wraps data in a SOCKS5 UDP request header and sends it to the relay.
// The relay strips the header and forwards the raw data to serverIP:serverPort.
func (u *socks5Uploader) send(data []byte) {
	u.mu.Lock()
	udpConn := u.udpConn
	relayAddr := u.relayAddr
	u.mu.Unlock()

	if udpConn == nil || relayAddr == nil {
		return
	}

	// SOCKS5 UDP request format (RFC 1928 §7):
	// +------+------+------+----------+----------+----------+
	// | RSV  | FRAG | ATYP | DST.ADDR | DST.PORT |   DATA   |
	// +------+------+------+----------+----------+----------+
	// |  2   |  1   |  1   |    4     |    2     | variable |
	// RSV=0x0000, FRAG=0x00 (no fragmentation), ATYP=0x01 (IPv4)
	pkt := make([]byte, 10+len(data))
	// pkt[0:2] = 0x00 0x00 (RSV)
	// pkt[2]   = 0x00 (FRAG)
	pkt[3] = 0x01 // ATYP = IPv4
	copy(pkt[4:8], u.serverIP)
	binary.BigEndian.PutUint16(pkt[8:10], u.serverPort)
	copy(pkt[10:], data)

	_ = udpConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	_, _ = udpConn.WriteTo(pkt, relayAddr)
}

// maintain keeps a SOCKS5 UDP ASSOCIATE session alive, reconnecting on failure.
func (u *socks5Uploader) maintain(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			u.clearConn()
			return
		}

		tcp, udp, relay, err := u.dial(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if u.log != nil {
				u.log.Warnf("\U0001F4E4 SOCKS5 upload: failed to connect to %s: %v (retry in 5s)", u.proxy, err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		u.setConn(tcp, udp, relay)

		if u.log != nil {
			u.log.Infof("\U0001F4E4 <green>SOCKS5 upload: connected via <cyan>%s</cyan> → relay <cyan>%s</cyan></green>", u.proxy, relay)
		}

		// Block in a background goroutine watching for TCP control connection death.
		// SOCKS5 spec requires the TCP connection to stay open while UDP relay is active.
		tcpDead := make(chan struct{})
		go func() {
			defer close(tcpDead)
			buf := make([]byte, 16)
			for {
				_ = tcp.SetReadDeadline(time.Now().Add(30 * time.Second))
				_, err := tcp.Read(buf)
				if err != nil {
					return
				}
			}
		}()

		select {
		case <-ctx.Done():
			u.clearConn()
			return
		case <-tcpDead:
			u.clearConn()
			if u.log != nil {
				u.log.Warnf("\U0001F4E4 SOCKS5 upload: connection to %s lost, reconnecting in 3s", u.proxy)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}
}

// dial performs the SOCKS5 handshake and UDP ASSOCIATE negotiation.
// Returns the TCP control conn, a bound UDP socket, and the relay address.
func (u *socks5Uploader) dial(ctx context.Context) (net.Conn, *net.UDPConn, *net.UDPAddr, error) {
	d := &net.Dialer{}
	tcp, err := d.DialContext(ctx, "tcp", u.proxy)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dial proxy TCP: %w", err)
	}

	_ = tcp.SetDeadline(time.Now().Add(10 * time.Second))

	// Greeting: version=5, nmethods=1, method=no-auth (0x00)
	if _, err := tcp.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		_ = tcp.Close()
		return nil, nil, nil, fmt.Errorf("write SOCKS5 greeting: %w", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(tcp, resp); err != nil {
		_ = tcp.Close()
		return nil, nil, nil, fmt.Errorf("read SOCKS5 greeting response: %w", err)
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		_ = tcp.Close()
		return nil, nil, nil, fmt.Errorf("proxy rejected no-auth: version=%d method=%d", resp[0], resp[1])
	}

	// UDP ASSOCIATE request: VER=5, CMD=3, RSV=0, ATYP=1(IPv4), ADDR=0.0.0.0, PORT=0
	// ADDR/PORT = 0 means "any source" — the proxy accepts UDP from our client addr.
	req := []byte{0x05, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if _, err := tcp.Write(req); err != nil {
		_ = tcp.Close()
		return nil, nil, nil, fmt.Errorf("write UDP ASSOCIATE request: %w", err)
	}

	// Reply: VER(1) REP(1) RSV(1) ATYP(1) BND.ADDR(var) BND.PORT(2)
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(tcp, hdr); err != nil {
		_ = tcp.Close()
		return nil, nil, nil, fmt.Errorf("read UDP ASSOCIATE reply header: %w", err)
	}
	if hdr[1] != 0x00 {
		_ = tcp.Close()
		return nil, nil, nil, fmt.Errorf("UDP ASSOCIATE rejected: REP=0x%02x", hdr[1])
	}

	var relayIP net.IP
	var relayPort uint16
	switch hdr[3] {
	case 0x01: // IPv4
		addr := make([]byte, 6)
		if _, err := io.ReadFull(tcp, addr); err != nil {
			_ = tcp.Close()
			return nil, nil, nil, fmt.Errorf("read relay IPv4 address: %w", err)
		}
		relayIP = net.IP(addr[:4])
		relayPort = binary.BigEndian.Uint16(addr[4:6])
	case 0x04: // IPv6
		addr := make([]byte, 18)
		if _, err := io.ReadFull(tcp, addr); err != nil {
			_ = tcp.Close()
			return nil, nil, nil, fmt.Errorf("read relay IPv6 address: %w", err)
		}
		relayIP = net.IP(addr[:16])
		relayPort = binary.BigEndian.Uint16(addr[16:18])
	default:
		_ = tcp.Close()
		return nil, nil, nil, fmt.Errorf("unsupported relay ATYP: 0x%02x", hdr[3])
	}

	// Some proxies return 0.0.0.0 — fall back to the proxy's own host.
	if relayIP.IsUnspecified() {
		host, _, err := net.SplitHostPort(u.proxy)
		if err == nil {
			if parsed := net.ParseIP(host); parsed != nil {
				relayIP = parsed
			}
		}
	}

	_ = tcp.SetDeadline(time.Time{}) // clear deadline for keep-alive monitoring

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		_ = tcp.Close()
		return nil, nil, nil, fmt.Errorf("open UDP send socket: %w", err)
	}

	relayAddr := &net.UDPAddr{IP: relayIP, Port: int(relayPort)}
	return tcp, udpConn, relayAddr, nil
}
