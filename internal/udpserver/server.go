// ==============================================================================
// MasterDns2Udp

// Github: https://github.com/retro1878/MasterDns2Udp
// Year: 2026
// ==============================================================================

package udpserver

import (
	"container/heap"
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"masterdns2udp/internal/config"
	dnsCache "masterdns2udp/internal/dnscache"
	domainMatcher "masterdns2udp/internal/domainmatcher"
	fragmentStore "masterdns2udp/internal/fragmentstore"
	"masterdns2udp/internal/logger"
	"masterdns2udp/internal/netutil"
	"masterdns2udp/internal/security"
	"masterdns2udp/internal/viotcp"
	VpnProto "masterdns2udp/internal/vpnproto"
)

const (
	mtuProbeModeRaw     = 0
	mtuProbeModeBase64  = 1
	mtuProbeCodeLength  = 4
	mtuProbeMetaLength  = mtuProbeCodeLength + 2
	mtuProbeUpMinSize   = 1 + mtuProbeCodeLength
	mtuProbeDownMinSize = mtuProbeUpMinSize + 2
	mtuProbeMinDownSize = VpnProto.SessionAcceptPayloadSize
	mtuProbeMaxDownSize = 4096
)

var preSessionPacketTypes = buildPreSessionPacketTypes()

type Server struct {
	cfg                      config.ServerConfig
	log                      *logger.Logger
	codec                    *security.Codec
	domainMatcher            *domainMatcher.Matcher
	sessions                 *sessionStore
	deferredDNSSession       *deferredSessionProcessor
	deferredConnectSession   *deferredSessionProcessor
	invalidCookieTracker     *invalidCookieTracker
	dnsCache                 *dnsCache.Store
	dnsResolveInflight       *dnsResolveInflightManager
	dnsUpstreamServers       []string
	dnsUpstreamBufferPool    sync.Pool
	dnsFragments             *fragmentStore.Store[dnsFragmentKey]
	socks5Fragments          *fragmentStore.Store[socks5FragmentKey]
	dnsFragmentTimeout       time.Duration
	resolveDNSQueryFn        func([]byte) ([]byte, error)
	dialStreamUpstreamFn     func(string, string, time.Duration) (net.Conn, error)
	uploadCompressionMask    uint8
	downloadCompressionMask  uint8
	dropLogIntervalNanos     int64
	invalidCookieWindow      time.Duration
	invalidCookieWindowNanos int64
	invalidCookieThreshold   int
	socksConnectTimeout      time.Duration
	useExternalSOCKS5        bool
	externalSOCKS5Address    string
	externalSOCKS5Auth       bool
	externalSOCKS5User       []byte
	externalSOCKS5Pass       []byte
	streamOutboundTTL        time.Duration
	streamOutboundMaxRetry   int
	mtuProbePayloadPool      sync.Pool
	packetPool               sync.Pool
	deferredInflightMu       sync.Mutex
	deferredInflight         map[uint64]struct{}
	deferredInflightIndex    map[uint8]map[uint16]map[uint64]struct{}
	immediateConnectedLog    throttledLogState
	invalidSessionDropLog    throttledLogState
	droppedPackets           atomic.Uint64
	lastDropLogUnix          atomic.Int64
	deferredDroppedPackets   atomic.Uint64
	lastDeferredDropLogUnix  atomic.Int64
	pongNonce                atomic.Uint32
	invalidDropMode          atomic.Uint32

	// UDP download channel (server→client, asymmetric)
	udpDownloadConn *net.UDPConn
	udpSendSignal   chan struct{}

	// UDP upload channel (client→server, raw bypass for SOCKS5 paths)
	udpUploadConn *net.UDPConn

	// Violated TCP download channel (server→client, parallel to UDP for GFW evasion)
	vioTCPSender *viotcp.Sender
}

type request struct {
	buf  []byte
	size int
	addr *net.UDPAddr
	conn *net.UDPConn
}

type postSessionValidation struct {
	record   *sessionRuntimeView
	response []byte
	ok       bool
}

func New(cfg config.ServerConfig, log *logger.Logger, codec *security.Codec) *Server {
	invalidCookieWindow := cfg.InvalidCookieWindow()
	if invalidCookieWindow <= 0 {
		invalidCookieWindow = 2 * time.Second
	}
	dnsFragmentTimeout := cfg.DNSFragmentAssemblyTimeout()
	if dnsFragmentTimeout <= 0 {
		dnsFragmentTimeout = 5 * time.Minute
	}
	dropLogInterval := cfg.DropLogInterval()
	if dropLogInterval <= 0 {
		dropLogInterval = 2 * time.Second
	}
	socksConnectTimeout := cfg.SOCKSConnectTimeout()
	if socksConnectTimeout <= 0 {
		socksConnectTimeout = 8 * time.Second
	}
	dnsDeferredWorkers, connectDeferredWorkers, dnsDeferredQueue, connectDeferredQueue := splitDeferredSessionPools(cfg.EffectiveDeferredSessionWorkers(), cfg.EffectiveDeferredSessionQueueLimit())
	sessions := newSessionStore(cfg.EffectiveSessionOrphanQueueInitialCap(), cfg.EffectiveStreamQueueInitialCapacity(), cfg.SessionInitReuseTTL(), cfg.RecentlyClosedStreamTTL(), cfg.RecentlyClosedStreamCap)
	sessions.maxActiveSessions = cfg.MaxAllowedClientActiveSessions
	sessions.maxActiveStreams = cfg.MaxAllowedClientActiveStreams
	return &Server{
		cfg:                    cfg,
		log:                    log,
		codec:                  codec,
		domainMatcher:          domainMatcher.New(cfg.Domain, cfg.MinVPNLabelLength),
		sessions:               sessions,
		deferredDNSSession:     newDeferredSessionProcessor(dnsDeferredWorkers, dnsDeferredQueue, log),
		deferredConnectSession: newDeferredSessionProcessor(connectDeferredWorkers, connectDeferredQueue, log),
		invalidCookieTracker:   newInvalidCookieTracker(),
		dnsCache: dnsCache.New(
			cfg.EffectiveDNSCacheMaxRecords(),
			time.Duration(cfg.DNSCacheTTLSeconds*float64(time.Second)),
			dnsFragmentTimeout,
		),
		dnsResolveInflight: newDNSResolveInflightManager(dnsFragmentTimeout),
		dnsUpstreamServers: append([]string(nil), cfg.DNSUpstreamServers...),
		dnsFragments:       fragmentStore.New[dnsFragmentKey](cfg.EffectiveDNSFragmentStoreCapacity()),
		socks5Fragments:    fragmentStore.New[socks5FragmentKey](cfg.EffectiveSOCKS5FragmentStoreCapacity()),
		dnsFragmentTimeout: dnsFragmentTimeout,
		dnsUpstreamBufferPool: sync.Pool{
			New: func() any {
				return make([]byte, 65535)
			},
		},
		dialStreamUpstreamFn: func(network string, address string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout(network, address, timeout)
		},
		uploadCompressionMask:    buildCompressionMask(cfg.SupportedUploadCompressionTypes),
		downloadCompressionMask:  buildCompressionMask(cfg.SupportedDownloadCompressionTypes),
		dropLogIntervalNanos:     dropLogInterval.Nanoseconds(),
		invalidCookieWindow:      invalidCookieWindow,
		invalidCookieWindowNanos: invalidCookieWindow.Nanoseconds(),
		invalidCookieThreshold:   cfg.InvalidCookieErrorThreshold,
		socksConnectTimeout:      socksConnectTimeout,
		useExternalSOCKS5:        cfg.UseExternalSOCKS5,
		externalSOCKS5Address:    net.JoinHostPort(cfg.ForwardIP, strconv.Itoa(cfg.ForwardPort)),
		externalSOCKS5Auth:       cfg.SOCKS5Auth,
		externalSOCKS5User:       []byte(cfg.SOCKS5User),
		externalSOCKS5Pass:       []byte(cfg.SOCKS5Pass),
		mtuProbePayloadPool: sync.Pool{
			New: func() any {
				return make([]byte, mtuProbeMaxDownSize)
			},
		},
		deferredInflight:      make(map[uint64]struct{}, 128),
		deferredInflightIndex: make(map[uint8]map[uint16]map[uint64]struct{}, 64),
		packetPool: sync.Pool{
			New: func() any {
				return make([]byte, cfg.MaxPacketSize)
			},
		},
		udpSendSignal: make(chan struct{}, 1),
	}
}

// signalUDPSend wakes the UDP sender goroutine non-blockingly.
func (s *Server) signalUDPSend() {
	select {
	case s.udpSendSignal <- struct{}{}:
	default:
	}
}

type throttledLogState struct {
	mu   sync.Mutex
	last map[string]int64
	heap throttledLogHeap
}

type throttledLogEntry struct {
	key  string
	seen int64
}

type throttledLogHeap []throttledLogEntry

func (h throttledLogHeap) Len() int { return len(h) }

func (h throttledLogHeap) Less(i, j int) bool {
	return h[i].seen < h[j].seen
}

func (h throttledLogHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *throttledLogHeap) Push(x any) {
	*h = append(*h, x.(throttledLogEntry))
}

func (h *throttledLogHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

const (
	throttledLogSoftCap = 1024
	throttledLogHardCap = 1536
)

func (s *throttledLogState) allow(key string, now time.Time, interval time.Duration) bool {
	if s == nil {
		return true
	}
	if interval <= 0 {
		interval = time.Second
	}

	nowUnixNano := now.UnixNano()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.last == nil {
		s.last = make(map[string]int64, 64)
	}

	last := s.last[key]

	if last != 0 && nowUnixNano-last < interval.Nanoseconds() {
		return false
	}

	s.last[key] = nowUnixNano
	heap.Push(&s.heap, throttledLogEntry{key: key, seen: nowUnixNano})

	if len(s.last) > 0 {
		s.pruneLocked(nowUnixNano, interval)
	}

	return true
}

func (s *throttledLogState) pruneLocked(nowUnixNano int64, interval time.Duration) {
	if s == nil || len(s.last) == 0 {
		return
	}

	cutoff := nowUnixNano - interval.Nanoseconds()
	for len(s.heap) > 0 {
		entry := s.heap[0]
		last, ok := s.last[entry.key]
		if !ok || last != entry.seen {
			heap.Pop(&s.heap)
			continue
		}
		if entry.seen > cutoff && len(s.last) <= throttledLogHardCap {
			break
		}
		delete(s.last, entry.key)
		heap.Pop(&s.heap)
	}

	for len(s.last) > throttledLogSoftCap && len(s.heap) > 0 {
		entry := heap.Pop(&s.heap).(throttledLogEntry)
		last, ok := s.last[entry.key]
		if !ok || last != entry.seen {
			continue
		}
		delete(s.last, entry.key)
	}
}

func splitDeferredSessionPools(totalWorkers int, totalQueue int) (dnsWorkers int, connectWorkers int, dnsQueue int, connectQueue int) {
	if totalWorkers <= 0 {
		totalWorkers = 1
	}
	if totalQueue <= 0 {
		totalQueue = 256
	}

	// DNS queries use a dedicated lightweight pool so connect-heavy work keeps
	// the full user-configured deferred capacity.
	dnsWorkers = 1
	connectWorkers = totalWorkers

	connectQueue = totalQueue
	dnsQueue = min(max(totalQueue/4, 64), 256)

	return dnsWorkers, connectWorkers, dnsQueue, connectQueue
}

func (s *Server) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	conns, err := s.openUDPListeners()
	if err != nil {
		return err
	}
	defer func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()

	s.log.Infof(
		"\U0001F4E1 <green>UDP Listener Ready, Addr: <cyan>%s</cyan>, Readers: <cyan>%d</cyan>, Workers: <cyan>%d</cyan>, Queue: <cyan>%d</cyan>, Sockets: <cyan>%d</cyan></green>",
		s.cfg.Address(),
		s.cfg.EffectiveUDPReaders(),
		s.cfg.EffectiveDNSRequestWorkers(),
		s.cfg.EffectiveMaxConcurrentRequests(),
		len(conns),
	)

	// Open the dedicated UDP download socket (server→client asymmetric channel).
	// UDP_DOWNLOAD_PORT = 0 disables this channel; use VIO_TCP_DOWNLOAD_PORT instead.
	if s.cfg.UDPDownloadPort > 0 {
		dlAddr := &net.UDPAddr{IP: net.ParseIP(s.cfg.UDPHost), Port: s.cfg.UDPDownloadPort}
		dlConn, dlErr := net.ListenUDP("udp", dlAddr)
		if dlErr != nil {
			return fmt.Errorf("failed to open UDP download socket on port %d: %w", s.cfg.UDPDownloadPort, dlErr)
		}
		s.udpDownloadConn = dlConn
		defer func() {
			_ = dlConn.Close()
			s.udpDownloadConn = nil
		}()
		s.log.Infof(
			"\U0001F4E4 <green>UDP Download Channel Ready on port <cyan>%d</cyan></green>",
			s.cfg.UDPDownloadPort,
		)
	} else {
		s.log.Infof("\U0001F4E4 <yellow>UDP Download Channel disabled (UDP_DOWNLOAD_PORT = 0)</yellow>")
	}

	// Open the raw UDP upload socket if configured (receives direct client→server uploads via SOCKS5 paths).
	var ulConn *net.UDPConn
	if s.cfg.UDPUploadPort > 0 {
		ulAddr := &net.UDPAddr{IP: net.ParseIP(s.cfg.UDPHost), Port: s.cfg.UDPUploadPort}
		ulConn, err = net.ListenUDP("udp", ulAddr)
		if err != nil {
			return fmt.Errorf("failed to open UDP upload socket on port %d: %w", s.cfg.UDPUploadPort, err)
		}
		s.udpUploadConn = ulConn
		defer func() {
			_ = ulConn.Close()
			s.udpUploadConn = nil
		}()
		s.log.Infof(
			"\U0001F4E4 <green>UDP Upload Channel Ready on port <cyan>%d</cyan></green>",
			s.cfg.UDPUploadPort,
		)
	}

	// Open the violated TCP sender if configured.
	if s.cfg.VioTCPDownloadPort > 0 {
		srcIPStr := s.cfg.VioTCPSourceIP
		var srcIP net.IP
		if srcIPStr != "" {
			srcIP = net.ParseIP(srcIPStr).To4()
		}
		if srcIP == nil {
			for _, ip := range netutil.LocalInterfaceIPs() {
				if parsed := net.ParseIP(ip).To4(); parsed != nil {
					srcIP = parsed
					break
				}
			}
		}
		if srcIP == nil {
			s.log.Warnf("\U0001F6AB <yellow>VioTCP: cannot determine source IP — violated TCP download channel disabled</yellow>")
		} else {
			sender, sErr := viotcp.NewSender(srcIP, uint16(s.cfg.VioTCPDownloadPort))
			if sErr != nil {
				s.log.Warnf("\U0001F6AB <yellow>VioTCP: failed to open raw socket: %v — violated TCP download channel disabled</yellow>", sErr)
			} else {
				s.vioTCPSender = sender
				defer func() {
					_ = sender.Close()
					s.vioTCPSender = nil
				}()
				s.log.Infof(
					"\U0001F4E1 <green>VioTCP Download Channel Ready (src <cyan>%s:%d</cyan>)</green>",
					srcIP,
					s.cfg.VioTCPDownloadPort,
				)
			}
		}
	}

	reqCh := make(chan request, s.cfg.EffectiveMaxConcurrentRequests())
	var workerWG sync.WaitGroup
	cleanupDone := make(chan struct{})

	go func() {
		defer close(cleanupDone)
		s.sessionCleanupLoop(runCtx)
	}()

	// Start the UDP sender goroutine.
	go s.runUDPSender(runCtx)

	// Start the raw UDP upload reader if the socket is open.
	if ulConn != nil {
		go s.runRawUDPUploadReader(runCtx, ulConn)
	}

	s.deferredDNSSession.Start(runCtx)
	s.deferredConnectSession.Start(runCtx)
	s.startDNSWorkers(runCtx, conns[0], reqCh, &workerWG)

	go func() {
		<-runCtx.Done()
		for _, conn := range conns {
			_ = conn.Close()
		}
		if s.udpDownloadConn != nil {
			_ = s.udpDownloadConn.Close()
		}
		if ulConn != nil {
			_ = ulConn.Close()
		}
	}()

	readErrCh := make(chan error, max(1, len(conns)))
	var readerWG sync.WaitGroup
	s.startReaders(runCtx, conns, reqCh, readErrCh, &readerWG)

	readerWG.Wait()
	close(reqCh)
	workerWG.Wait()
	cancel()
	<-cleanupDone

	if ctx.Err() != nil {
		return ctx.Err()
	}

	select {
	case err := <-readErrCh:
		return err
	default:
		return nil
	}
}
