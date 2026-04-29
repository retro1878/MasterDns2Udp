#!/usr/bin/env bash
# =============================================================================
# MasterDns2Udp — Installer
# Supports: server (Iran side) and client (outside side)
# OS: Linux x86_64 (amd64)
# =============================================================================

# ── curl|bash bootstrap ───────────────────────────────────────────────────────
# When piped via "curl URL | bash", bash reads the script from stdin, which
# means interactive read commands compete with the pipe for the same fd.
# Fix: drain the rest of the pipe into a temp file and re-exec from it.
# BASH_SOURCE[0] is empty when bash is reading from stdin (pipe), non-empty
# when reading from a file — so the re-exec'd process skips this block.
if [[ -z "${BASH_SOURCE[0]:-}" ]]; then
    _T=$(mktemp /tmp/md2u-install.XXXXXX.sh)
    cat > "$_T"          # consume remaining pipe (rest of script)
    exec bash "$_T" "$@" # re-exec from file; stdin now free for /dev/tty
fi

set -euo pipefail

# ── Colours ───────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

ok()     { echo -e "${GREEN}✔${NC}  $*"; }
info()   { echo -e "${CYAN}ℹ${NC}  $*"; }
warn()   { echo -e "${YELLOW}⚠${NC}  $*"; }
die()    { echo -e "${RED}✘  $*${NC}" >&2; exit 1; }
banner() { echo -e "\n${BOLD}${CYAN}── $* ──${NC}"; }

# ── Constants ─────────────────────────────────────────────────────────────────
REPO_RAW="https://raw.githubusercontent.com/retro1878/MasterDns2Udp/claude/setup-dns-tunneling-QICmN"
INSTALL_DIR="/opt/masterdns2udp"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || pwd)"

# ── Ensure interactive prompts work even when piped via: curl ... | bash ──────
if [[ ! -t 0 ]]; then
    exec < /dev/tty || die "Cannot open /dev/tty for interactive input. Download the script and run it directly: bash install.sh"
fi

# ── Helpers ───────────────────────────────────────────────────────────────────
ask() {
    local __var=$1 __prompt=$2 __default=${3:-}
    local __hint=""
    [[ -n $__default ]] && __hint=" [${__default}]"
    while true; do
        read -rp "$(echo -e "${BOLD}${__prompt}${__hint}: ${NC}")" __input || __input=""
        __input="${__input:-$__default}"
        if [[ -n $__input ]]; then
            printf -v "$__var" '%s' "$__input"
            return
        fi
        warn "Value required."
    done
}

ask_optional() {
    local __var=$1 __prompt=$2 __default=${3:-}
    local __hint=""
    [[ -n $__default ]] && __hint=" [${__default}]"
    read -rp "$(echo -e "${BOLD}${__prompt}${__hint}: ${NC}")" __input || __input=""
    printf -v "$__var" '%s' "${__input:-$__default}"
}

ask_yn() {
    local __prompt=$1 __default=${2:-Y}
    while true; do
        local __hint="Y/n"; [[ $__default == N ]] && __hint="y/N"
        read -rp "$(echo -e "${BOLD}${__prompt} [${__hint}]: ${NC}")" __ans || __ans=""
        __ans="${__ans:-$__default}"
        case "${__ans,,}" in
            y|yes) return 0 ;;
            n|no)  return 1 ;;
            *) warn "Please answer y or n." ;;
        esac
    done
}

require_root() {
    [[ $EUID -eq 0 ]] || die "This installer must be run as root (sudo $0)."
}

check_arch() {
    local arch; arch=$(uname -m)
    [[ $arch == x86_64 ]] || die "Only x86_64 (amd64) is supported. Detected: $arch"
}

generate_key() {
    if command -v openssl &>/dev/null; then
        openssl rand -base64 32
    else
        dd if=/dev/urandom bs=32 count=1 2>/dev/null | base64 | tr -d '\n'
    fi
}

download_binary() {
    local name=$1 dest=$2
    info "Downloading ${name} from GitHub..."
    if command -v curl &>/dev/null; then
        curl -fsSL "${REPO_RAW}/${name}" -o "$dest" \
            || die "Failed to download ${name} with curl."
    elif command -v wget &>/dev/null; then
        wget -q "${REPO_RAW}/${name}" -O "$dest" \
            || die "Failed to download ${name} with wget."
    else
        die "Neither curl nor wget is available. Install one and retry."
    fi
    chmod +x "$dest"
    ok "Downloaded ${name}"
}

ensure_binary() {
    local name=$1 dest="${INSTALL_DIR}/${1}"
    if [[ -x $dest ]]; then
        if ask_yn "  ${name} already exists in ${INSTALL_DIR}. Re-download?" N; then
            download_binary "$name" "$dest"
        else
            ok "Using existing ${name}"
        fi
        return
    fi
    if [[ -x "${SCRIPT_DIR}/${name}" ]]; then
        cp "${SCRIPT_DIR}/${name}" "$dest"
        chmod +x "$dest"
        ok "Copied ${name} from script directory"
        return
    fi
    download_binary "$name" "$dest"
}

open_port_ufw() {
    local port=$1 proto=${2:-udp}
    if command -v ufw &>/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
        ufw allow "${port}/${proto}" >/dev/null \
            && ok "ufw: opened ${port}/${proto}" \
            || warn "ufw: could not open ${port}/${proto} (may already be open)"
    fi
}

open_port_firewalld() {
    local port=$1 proto=${2:-udp}
    if command -v firewall-cmd &>/dev/null && firewall-cmd --state &>/dev/null; then
        firewall-cmd --permanent --add-port="${port}/${proto}" >/dev/null \
            || warn "firewalld: could not add ${port}/${proto} (may already be open)"
        firewall-cmd --reload >/dev/null \
            || warn "firewalld: reload failed — rule may not be active"
        ok "firewalld: opened ${port}/${proto}"
    fi
}

open_port() { open_port_ufw "$@"; open_port_firewalld "$@"; }

write_systemd_service() {
    local unit=$1 desc=$2 binary=$3 config=$4 extra_after=${5:-}
    local after="network.target"
    [[ -n $extra_after ]] && after="${after} ${extra_after}"
    cat > "/etc/systemd/system/${unit}.service" <<SVCEOF
[Unit]
Description=${desc}
After=${after}
Wants=network-online.target

[Service]
Type=simple
ExecStart=${binary} -config ${config}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SVCEOF
    systemctl daemon-reload \
        || warn "systemctl daemon-reload failed — service file may not be loaded"
    ok "Created /etc/systemd/system/${unit}.service"
}

enable_and_start() {
    local unit=$1
    if systemctl enable --now "${unit}" >/dev/null 2>&1; then
        ok "Service ${unit} enabled and started"
    else
        warn "systemctl enable --now ${unit} returned an error"
    fi
    sleep 1
    systemctl is-active --quiet "$unit" \
        && ok "${unit} is running" \
        || warn "${unit} did not start cleanly — check: journalctl -u ${unit} -n 40"
}

# =============================================================================
# MAIN
# =============================================================================
echo
echo -e "${BOLD}${CYAN}╔══════════════════════════════════════╗${NC}"
echo -e "${BOLD}${CYAN}║     MasterDns2Udp  Installer         ║${NC}"
echo -e "${BOLD}${CYAN}╚══════════════════════════════════════╝${NC}"
echo

require_root
check_arch

banner "Installation type"
echo "  1) Server  (runs on the Iran server — receives DNS tunnel traffic)"
echo "  2) Client  (runs on the outside server — local SOCKS5 proxy)"
echo "  3) Update  (re-download binaries for an existing installation)"
echo
MODE=""
while [[ $MODE != "1" && $MODE != "2" && $MODE != "3" ]]; do
    read -rp "$(echo -e "${BOLD}Choose [1/2/3]: ${NC}")" MODE || MODE=""
done

mkdir -p "$INSTALL_DIR"

# =============================================================================
# UPDATE
# =============================================================================
if [[ $MODE == "3" ]]; then
    banner "Updating binaries"
    found=0
    for binary in masterdns2udp-server masterdns2udp-client; do
        dest="${INSTALL_DIR}/${binary}"
        unit="${binary}"
        if [[ -x "$dest" ]]; then
            download_binary "$binary" "$dest"
            found=1
            if systemctl is-active --quiet "$unit" 2>/dev/null; then
                systemctl restart "$unit" \
                    && ok "Service ${unit} restarted" \
                    || warn "Failed to restart ${unit} — check: journalctl -u ${unit} -n 20"
            elif systemctl is-enabled --quiet "$unit" 2>/dev/null; then
                info "Service ${unit} is enabled but not running — starting it"
                systemctl start "$unit" \
                    && ok "Service ${unit} started" \
                    || warn "Failed to start ${unit} — check: journalctl -u ${unit} -n 20"
            fi
        else
            info "${binary} not found in ${INSTALL_DIR} — skipping"
        fi
    done
    if [[ $found -eq 0 ]]; then
        warn "No existing binaries found in ${INSTALL_DIR}."
        warn "Run option 1 (server) or 2 (client) to do a fresh install first."
        exit 1
    fi
    banner "Done"
    echo
    ok "Binaries updated in ${INSTALL_DIR}"
    echo
    echo -e "  Check status : ${CYAN}systemctl status masterdns2udp-server${NC}"
    echo -e "               : ${CYAN}systemctl status masterdns2udp-client${NC}"
    echo
    exit 0
fi

[[ $MODE == "1" ]] && ROLE="server" || ROLE="client"
echo
ok "Installing as: ${ROLE}"

# =============================================================================
# SERVER
# =============================================================================
if [[ $ROLE == "server" ]]; then

    banner "Server configuration"
    ask          DOMAIN      "Tunnel domain (e.g. vpn.example.com)"
    ask_optional DNS_PORT    "DNS listen port (UDP_PORT)"        "53"
    ask_optional UPSTREAM    "DNS upstream servers (comma-sep)"  "8.8.8.8:53,1.1.1.1:53"
    ask_optional LOG_LEVEL   "Log level (DEBUG/INFO/WARN/ERROR)" "INFO"

    banner "Download channel"
    echo "  Mode A — Raw UDP      : plain UDP datagrams to the client's public IP."
    echo "  Mode B — Violated TCP : DPI-evading TCP segments (for Irancell IP ranges)."
    echo "                          Requires root/CAP_NET_RAW. No firewall rule needed."
    echo "  Mode C — Both         : parallel paths; client ARQ deduplicates."
    echo "  Set a port to 0 to disable that mode."
    echo
    ask_optional UDP_DL_PORT  "UDP download port     (Mode A, 0=off)" "5555"
    ask_optional VIO_DL_PORT  "VioTCP download port  (Mode B, 0=off)" "0"
    VIO_SRC_IP=""
    if [[ $VIO_DL_PORT != "0" ]]; then
        ask_optional VIO_SRC_IP \
            "Server public IPv4 for VioTCP packet headers (blank = auto-detect)" ""
    fi
    ask_optional UL_PORT "SOCKS5 upload receive port (0=off)" "0"

    banner "Encryption key"
    echo "  Press Enter to generate a fresh key, or paste an existing one."
    echo
    read -rp "$(echo -e "${BOLD}Paste existing key (or Enter to generate): ${NC}")" ENC_KEY || ENC_KEY=""
    if [[ -z $ENC_KEY ]]; then
        ENC_KEY=$(generate_key)
        echo
        echo -e "${BOLD}${YELLOW}Generated key — copy this to the client installer:${NC}"
        echo -e "${GREEN}${ENC_KEY}${NC}"
        echo
        warn "Save this key now."
        read -rp "Press Enter to continue..." || true
    fi
    printf '%s\n' "$ENC_KEY" > "${INSTALL_DIR}/encrypt_key.txt"
    chmod 600 "${INSTALL_DIR}/encrypt_key.txt"
    ok "encrypt_key.txt written"

    # Build upstream TOML array
    UPSTREAM_TOML="["
    IFS=',' read -ra US_ARRAY <<< "$UPSTREAM"
    for s in "${US_ARRAY[@]}"; do
        s="${s#"${s%%[![:space:]]*}"}"; s="${s%"${s##*[![:space:]]}"}"
        UPSTREAM_TOML+="\"${s}\", "
    done
    UPSTREAM_TOML="${UPSTREAM_TOML%, }]"

    CONFIG_PATH="${INSTALL_DIR}/server.toml"
    cat > "$CONFIG_PATH" <<CFGEOF
# MasterDns2Udp — Server Config (generated by installer)

UDP_HOST = "0.0.0.0"
UDP_PORT = ${DNS_PORT}

# Download channel  (Mode A=UDP, Mode B=VioTCP, Mode C=both; 0=disabled)
UDP_DOWNLOAD_PORT     = ${UDP_DL_PORT}
VIO_TCP_DOWNLOAD_PORT = ${VIO_DL_PORT}
VIO_TCP_SOURCE_IP     = "${VIO_SRC_IP}"

UDP_UPLOAD_PORT = ${UL_PORT}

DOMAIN               = ["${DOMAIN}"]
MIN_VPN_LABEL_LENGTH = 3

DATA_ENCRYPTION_METHOD = 1
ENCRYPTION_KEY_FILE    = "${INSTALL_DIR}/encrypt_key.txt"

DNS_UPSTREAM_SERVERS = ${UPSTREAM_TOML}
DNS_UPSTREAM_TIMEOUT = 4.0

PROTOCOL_TYPE       = "SOCKS5"
USE_EXTERNAL_SOCKS5 = false
SOCKS5_AUTH         = false

SUPPORTED_UPLOAD_COMPRESSION_TYPES   = [0, 1, 2, 3]
SUPPORTED_DOWNLOAD_COMPRESSION_TYPES = [0, 1, 2, 3]

SESSION_TIMEOUT_SECONDS = 300.0
ARQ_WINDOW_SIZE         = 800
ARQ_INITIAL_RTO_SECONDS = 1.0
ARQ_MAX_RTO_SECONDS     = 5.0

LOG_LEVEL = "${LOG_LEVEL}"
CFGEOF
    ok "server.toml written → ${CONFIG_PATH}"

    banner "Binary"
    ensure_binary "masterdns2udp-server"

    banner "Firewall"
    PORTS_TO_OPEN=("$DNS_PORT")
    [[ $UDP_DL_PORT != "0" ]] && PORTS_TO_OPEN+=("$UDP_DL_PORT")
    [[ $UL_PORT     != "0" ]] && PORTS_TO_OPEN+=("$UL_PORT")
    PORTS_STR=$(IFS=', '; echo "${PORTS_TO_OPEN[*]}")
    if ask_yn "  Open UDP port(s) ${PORTS_STR} in firewall (ufw/firewalld)?"; then
        for p in "${PORTS_TO_OPEN[@]}"; do open_port "$p" udp; done
    fi
    if [[ $VIO_DL_PORT != "0" ]]; then
        info "VioTCP: server sends OUT on port ${VIO_DL_PORT} — no inbound firewall rule needed."
    fi

    banner "Systemd service"
    write_systemd_service \
        "masterdns2udp-server" "MasterDns2Udp Server" \
        "${INSTALL_DIR}/masterdns2udp-server" "$CONFIG_PATH"
    enable_and_start "masterdns2udp-server"

    banner "Done"
    echo
    ok "Server installed in ${INSTALL_DIR}"
    echo
    echo -e "  ${BOLD}Encryption key (paste into client installer):${NC}"
    echo -e "  ${GREEN}$(cat "${INSTALL_DIR}/encrypt_key.txt")${NC}"
    echo
    echo -e "  ${BOLD}Reminder:${NC} point the NS record for ${CYAN}${DOMAIN}${NC} to this server's IP."
    echo
    echo -e "  Check status : ${CYAN}systemctl status masterdns2udp-server${NC}"
    echo -e "  View logs    : ${CYAN}journalctl -u masterdns2udp-server -f${NC}"
    echo

# =============================================================================
# CLIENT
# =============================================================================
else

    banner "Client configuration"
    ask          DOMAIN       "Tunnel domain (must match server)"
    ask_optional LISTEN_PORT  "Local SOCKS5 listen port"               "18000"
    ask_optional LOG_LEVEL    "Log level (DEBUG/INFO/WARN/ERROR)"       "INFO"

    banner "Iran server identity"
    echo "  The server's public IPv4 is required for:"
    echo "    • Violated TCP download — raw socket filters inbound packets by source IP"
    echo "    • SOCKS5 upload paths   — upload packets are addressed to this IP"
    echo "  Leave blank if using Mode A (UDP download) only."
    echo
    ask_optional SERVER_IP "Iran server public IPv4 (blank = UDP-only)" ""

    banner "Download channel"
    echo "  Mode A — Raw UDP      : server sends plain UDP to your public IP."
    echo "  Mode B — Violated TCP : DPI-evading segments (requires SERVER_IP above)."
    echo "  Mode C — Both         : parallel paths; ARQ deduplicates."
    echo "  Set a port to 0 (or leave IP blank) to disable that mode."
    echo

    echo "  ── Mode A: Raw UDP ──────────────────────────────────────────────────"
    ask_optional UDP_DL_IP "Your public IPv4 for UDP download (blank = disable Mode A)" ""
    UDP_DL_PORT="0"
    if [[ -n $UDP_DL_IP ]]; then
        ask_optional UDP_DL_PORT "UDP download port (must match server UDP_DOWNLOAD_PORT)" "5555"
    fi

    echo
    echo "  ── Mode B: Violated TCP ─────────────────────────────────────────────"
    VIO_DL_PORT="0"
    VIO_SRV_PORT="0"
    if [[ -z $SERVER_IP ]]; then
        info "Skipping VioTCP — SERVER_IP not set."
    else
        echo "  Choose any closed port on THIS machine (nothing must listen on it)."
        echo "  Do NOT open it in the firewall — the kernel RST is harmless and expected."
        ask_optional VIO_DL_PORT \
            "Closed port on this machine for VioTCP (0 = disable Mode B)" "0"
        if [[ $VIO_DL_PORT != "0" ]]; then
            echo "  Enter the same value you set for VIO_TCP_DOWNLOAD_PORT on the server."
            echo "  The client's raw socket uses it to recognise tunnel packets."
            ask_optional VIO_SRV_PORT \
                "Server's VIO_TCP_DOWNLOAD_PORT (source port the server sends from)" "0"
        fi
    fi

    banner "Encryption key"
    echo "  Paste the key shown at the end of the server installer."
    echo
    ENC_KEY=""
    while [[ -z $ENC_KEY ]]; do
        read -rp "$(echo -e "${BOLD}Encryption key: ${NC}")" ENC_KEY || ENC_KEY=""
        [[ -z $ENC_KEY ]] && warn "Key is required."
    done

    banner "DNS resolvers"
    echo "  Iranian public DNS resolvers the client sends tunnel queries through."
    echo "  Format: IP:PORT  (one per line; blank line to finish)"
    echo "  Example: 178.22.122.100:53"
    echo
    RESOLVERS=()
    while true; do
        read -rp "$(echo -e "${BOLD}  Resolver (blank to finish): ${NC}")" R || R=""
        [[ -z $R ]] && break
        if [[ $R =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:[0-9]+$ ]]; then
            RESOLVERS+=("$R"); ok "  Added: $R"
        else
            warn "  Invalid format — use IP:PORT (e.g. 178.22.122.100:53)"
        fi
    done
    if [[ ${#RESOLVERS[@]} -eq 0 ]]; then
        warn "No resolvers entered. Edit ${INSTALL_DIR}/client_resolvers.txt before starting."
        RESOLVERS=("0.0.0.0:53  # REPLACE with a real Iranian resolver")
    fi
    RESOLVERS_PATH="${INSTALL_DIR}/client_resolvers.txt"
    printf '%s\n' "${RESOLVERS[@]}" > "$RESOLVERS_PATH"
    ok "client_resolvers.txt written → ${RESOLVERS_PATH}"

    banner "SOCKS5 upload paths (optional)"
    echo "  Mirror upload packets through local SOCKS5 proxies (e.g. ArvanCloud reverse"
    echo "  tunnels) as an extra upload path alongside the DNS channel."
    echo "  Each proxy must support UDP ASSOCIATE. Requires SERVER_IP to be set."
    echo
    SOCKS5_PROXIES_TOML="[]"
    UL_PORT="0"
    if [[ -z $SERVER_IP ]]; then
        info "Skipping — SERVER_IP not set."
    else
        ask_optional UL_PORT \
            "UDP upload port on the server (must match server UDP_UPLOAD_PORT, 0=skip)" "0"
        if [[ $UL_PORT != "0" ]]; then
            echo "  Enter each local SOCKS5 proxy (IP:PORT; blank to finish)."
            SOCKS5_LIST=()
            while true; do
                read -rp "$(echo -e "${BOLD}  Proxy (blank to finish): ${NC}")" P || P=""
                [[ -z $P ]] && break
                if [[ $P =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:[0-9]+$ ]]; then
                    SOCKS5_LIST+=("$P"); ok "  Added: $P"
                else
                    warn "  Invalid format — use IP:PORT (e.g. 127.0.0.1:13000)"
                fi
            done
            if [[ ${#SOCKS5_LIST[@]} -gt 0 ]]; then
                SOCKS5_PROXIES_TOML="["
                for p in "${SOCKS5_LIST[@]}"; do SOCKS5_PROXIES_TOML+="\"${p}\", "; done
                SOCKS5_PROXIES_TOML="${SOCKS5_PROXIES_TOML%, }]"
            fi
        fi
    fi

    CONFIG_PATH="${INSTALL_DIR}/client.toml"
    cat > "$CONFIG_PATH" <<CFGEOF
# MasterDns2Udp — Client Config (generated by installer)

PROTOCOL_TYPE = "SOCKS5"
LISTEN_IP     = "127.0.0.1"
LISTEN_PORT   = ${LISTEN_PORT}

SERVER_IP = "${SERVER_IP}"

# Download channel  (Mode A=UDP, Mode B=VioTCP, Mode C=both; 0=disabled)
UDP_DOWNLOAD_IP       = "${UDP_DL_IP}"
UDP_DOWNLOAD_PORT     = ${UDP_DL_PORT}
VIO_TCP_DOWNLOAD_PORT = ${VIO_DL_PORT}
VIO_TCP_SERVER_PORT   = ${VIO_SRV_PORT}

UDP_UPLOAD_PORT       = ${UL_PORT}
UPLOAD_SOCKS5_PROXIES = ${SOCKS5_PROXIES_TOML}

DOMAINS = ["${DOMAIN}"]

DATA_ENCRYPTION_METHOD = 1
ENCRYPTION_KEY         = "${ENC_KEY}"

UPLOAD_COMPRESSION_TYPE   = 0
DOWNLOAD_COMPRESSION_TYPE = 0
COMPRESSION_MIN_SIZE      = 120

MIN_UPLOAD_MTU   = 38
MAX_UPLOAD_MTU   = 150
MIN_DOWNLOAD_MTU = 100
MAX_DOWNLOAD_MTU = 500

PACKET_DUPLICATION_COUNT       = 2
SETUP_PACKET_DUPLICATION_COUNT = 2

RX_TX_WORKERS           = 4
ARQ_WINDOW_SIZE         = 600
ARQ_INITIAL_RTO_SECONDS = 1.0
ARQ_MAX_RTO_SECONDS     = 5.0

PING_AGGRESSIVE_INTERVAL_SECONDS = 0.1
PING_LAZY_INTERVAL_SECONDS       = 0.75

LOG_LEVEL = "${LOG_LEVEL}"
CFGEOF
    ok "client.toml written → ${CONFIG_PATH}"

    banner "Binary"
    ensure_binary "masterdns2udp-client"

    banner "Firewall"
    if [[ -n $UDP_DL_IP && $UDP_DL_PORT != "0" ]]; then
        if ask_yn "  Open UDP port ${UDP_DL_PORT} for UDP download (ufw/firewalld)?"; then
            open_port "$UDP_DL_PORT" udp
        fi
    else
        info "No UDP download port to open."
    fi
    if [[ $VIO_DL_PORT != "0" ]]; then
        info "VioTCP: port ${VIO_DL_PORT} must stay CLOSED — do NOT open it in the firewall."
    fi

    banner "Systemd service"
    write_systemd_service \
        "masterdns2udp-client" "MasterDns2Udp Client" \
        "${INSTALL_DIR}/masterdns2udp-client" "$CONFIG_PATH"
    enable_and_start "masterdns2udp-client"

    banner "Done"
    echo
    ok "Client installed in ${INSTALL_DIR}"
    echo
    echo -e "  ${BOLD}SOCKS5 proxy:${NC}  ${CYAN}127.0.0.1:${LISTEN_PORT}${NC}"
    echo -e "  Point your browser or app at this address."
    echo
    echo -e "  Check status  : ${CYAN}systemctl status masterdns2udp-client${NC}"
    echo -e "  View logs     : ${CYAN}journalctl -u masterdns2udp-client -f${NC}"
    echo -e "  Edit resolvers: ${CYAN}${INSTALL_DIR}/client_resolvers.txt${NC}"
    echo
fi
