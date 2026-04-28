#!/usr/bin/env bash
# =============================================================================
# MasterDns2Udp — Installer
# Supports: server (Iran side) and client (outside side)
# OS: Linux x86_64 (amd64)
# =============================================================================
set -euo pipefail

# ── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

ok()   { echo -e "${GREEN}✔${NC}  $*"; }
info() { echo -e "${CYAN}ℹ${NC}  $*"; }
warn() { echo -e "${YELLOW}⚠${NC}  $*"; }
die()  { echo -e "${RED}✘  $*${NC}" >&2; exit 1; }
banner() { echo -e "\n${BOLD}${CYAN}── $* ──${NC}"; }

# ── Constants ─────────────────────────────────────────────────────────────────
REPO_RAW="https://raw.githubusercontent.com/retro1878/MasterDns2Udp/claude/setup-dns-tunneling-QICmN"
INSTALL_DIR="/opt/masterdns2udp"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-}")" && pwd)"

# ── Helpers ───────────────────────────────────────────────────────────────────
ask() {
    # ask <variable> <prompt> [default]
    local __var=$1 __prompt=$2 __default=${3:-}
    local __hint=""
    [[ -n $__default ]] && __hint=" [${__default}]"
    while true; do
        read -rp "$(echo -e "${BOLD}${__prompt}${__hint}: ${NC}")" __input
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
    read -rp "$(echo -e "${BOLD}${__prompt}${__hint}: ${NC}")" __input
    printf -v "$__var" '%s' "${__input:-$__default}"
}

ask_yn() {
    # ask_yn <prompt> [Y|N]  → returns 0 for yes, 1 for no
    local __prompt=$1 __default=${2:-Y}
    while true; do
        local __hint="Y/n"; [[ $__default == N ]] && __hint="y/N"
        read -rp "$(echo -e "${BOLD}${__prompt} [${__hint}]: ${NC}")" __ans
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
        dd if=/dev/urandom bs=24 count=1 2>/dev/null | base64
    fi
}

download_binary() {
    local name=$1 dest=$2
    info "Downloading ${name} from GitHub..."
    if command -v curl &>/dev/null; then
        curl -fsSL "${REPO_RAW}/${name}" -o "$dest"
    elif command -v wget &>/dev/null; then
        wget -q "${REPO_RAW}/${name}" -O "$dest"
    else
        die "Neither curl nor wget is available. Install one and retry."
    fi
    chmod +x "$dest"
    ok "Downloaded ${name}"
}

ensure_binary() {
    local name=$1 dest="${INSTALL_DIR}/${1}"

    # Already installed?
    if [[ -x $dest ]]; then
        if ask_yn "  ${name} already exists in ${INSTALL_DIR}. Re-download?" N; then
            download_binary "$name" "$dest"
        else
            ok "Using existing ${name}"
        fi
        return
    fi

    # Sitting next to the script?
    if [[ -x "${SCRIPT_DIR}/${name}" ]]; then
        cp "${SCRIPT_DIR}/${name}" "$dest"
        chmod +x "$dest"
        ok "Copied ${name} from script directory"
        return
    fi

    # Download it
    download_binary "$name" "$dest"
}

open_port_ufw() {
    local port=$1 proto=${2:-udp}
    if command -v ufw &>/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
        ufw allow "${port}/${proto}" &>/dev/null && ok "ufw: opened ${port}/${proto}"
    fi
}

open_port_firewalld() {
    local port=$1 proto=${2:-udp}
    if command -v firewall-cmd &>/dev/null && firewall-cmd --state &>/dev/null; then
        firewall-cmd --permanent --add-port="${port}/${proto}" &>/dev/null
        firewall-cmd --reload &>/dev/null
        ok "firewalld: opened ${port}/${proto}"
    fi
}

open_port() { open_port_ufw "$@"; open_port_firewalld "$@"; }

write_systemd_service() {
    local unit=$1 desc=$2 binary=$3 config=$4 extra_after=${5:-}
    local after="network.target"
    [[ -n $extra_after ]] && after="${after} ${extra_after}"
    cat > "/etc/systemd/system/${unit}.service" <<EOF
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
EOF
    systemctl daemon-reload
    ok "Created /etc/systemd/system/${unit}.service"
}

enable_and_start() {
    local unit=$1
    systemctl enable --now "${unit}" &>/dev/null
    ok "Service ${unit} enabled and started"
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

# ── Mode selection ────────────────────────────────────────────────────────────
banner "Installation type"
echo "  1) Server  (runs on the Iran server — receives DNS tunnel traffic)"
echo "  2) Client  (runs on the outside server — local SOCKS5 proxy)"
echo
MODE=""
while [[ $MODE != "1" && $MODE != "2" ]]; do
    read -rp "$(echo -e "${BOLD}Choose [1/2]: ${NC}")" MODE
done
[[ $MODE == "1" ]] && ROLE="server" || ROLE="client"
echo
ok "Installing as: ${ROLE}"

# ── Prepare install directory ─────────────────────────────────────────────────
mkdir -p "$INSTALL_DIR"

# =============================================================================
# SERVER
# =============================================================================
if [[ $ROLE == "server" ]]; then

    banner "Server configuration"

    ask         DOMAIN          "Tunnel domain (e.g. vpn.example.com)"
    ask_optional DNS_PORT       "DNS listen port (UDP_PORT)"              "53"
    ask_optional DL_PORT        "UDP download port (UDP_DOWNLOAD_PORT)"   "5555"
    ask_optional UPSTREAM       "DNS upstream servers (comma-separated)"  "8.8.8.8:53,1.1.1.1:53"
    ask_optional LOG_LEVEL      "Log level (DEBUG/INFO/WARN/ERROR)"       "INFO"

    # Encryption key
    banner "Encryption key"
    echo "  You can generate a new key now, or paste an existing one."
    echo "  If you already have a key (from a previous install), paste it."
    echo "  Otherwise press Enter to generate a fresh key."
    echo
    read -rp "$(echo -e "${BOLD}Paste existing key (or Enter to generate): ${NC}")" ENC_KEY
    if [[ -z $ENC_KEY ]]; then
        ENC_KEY=$(generate_key)
        echo
        echo -e "${BOLD}${YELLOW}Generated encryption key (copy this to your client config):${NC}"
        echo -e "${GREEN}${ENC_KEY}${NC}"
        echo
        warn "Save this key — you will need to paste it into the client installer."
        read -rp "Press Enter to continue..."
    fi

    # Write key file
    echo "$ENC_KEY" > "${INSTALL_DIR}/encrypt_key.txt"
    chmod 600 "${INSTALL_DIR}/encrypt_key.txt"
    ok "encrypt_key.txt written"

    # Build upstream DNS list for TOML
    IFS=',' read -ra US_ARRAY <<< "$UPSTREAM"
    UPSTREAM_TOML="["
    for s in "${US_ARRAY[@]}"; do
        s="$(echo "$s" | xargs)"  # trim spaces
        UPSTREAM_TOML+="\"${s}\", "
    done
    UPSTREAM_TOML="${UPSTREAM_TOML%, }]"

    # Write server.toml
    CONFIG_PATH="${INSTALL_DIR}/server.toml"
    cat > "$CONFIG_PATH" <<EOF
# MasterDns2Udp — Server Config (generated by installer)

UDP_HOST         = "0.0.0.0"
UDP_PORT         = ${DNS_PORT}
UDP_DOWNLOAD_PORT = ${DL_PORT}

DOMAIN = ["${DOMAIN}"]
MIN_VPN_LABEL_LENGTH = 3

DATA_ENCRYPTION_METHOD = 1
ENCRYPTION_KEY_FILE    = "${INSTALL_DIR}/encrypt_key.txt"

DNS_UPSTREAM_SERVERS = ${UPSTREAM_TOML}
DNS_UPSTREAM_TIMEOUT = 4.0

PROTOCOL_TYPE = "SOCKS5"
USE_EXTERNAL_SOCKS5 = false
SOCKS5_AUTH         = false

SUPPORTED_UPLOAD_COMPRESSION_TYPES   = [0, 1, 2, 3]
SUPPORTED_DOWNLOAD_COMPRESSION_TYPES = [0, 1, 2, 3]

SESSION_TIMEOUT_SECONDS = 300.0
ARQ_WINDOW_SIZE         = 800
ARQ_INITIAL_RTO_SECONDS = 1.0
ARQ_MAX_RTO_SECONDS     = 5.0

LOG_LEVEL = "${LOG_LEVEL}"
EOF
    ok "server.toml written → ${CONFIG_PATH}"

    # Binary
    banner "Binary"
    ensure_binary "masterdns2udp-server"

    # Firewall
    banner "Firewall"
    if ask_yn "  Open UDP ports ${DNS_PORT} and ${DL_PORT} in firewall (ufw/firewalld)?"; then
        open_port "$DNS_PORT"  udp
        open_port "$DL_PORT"   udp
    fi

    # Systemd
    banner "Systemd service"
    write_systemd_service \
        "masterdns2udp-server" \
        "MasterDns2Udp Server" \
        "${INSTALL_DIR}/masterdns2udp-server" \
        "$CONFIG_PATH"
    enable_and_start "masterdns2udp-server"

    # Summary
    banner "Done"
    echo
    ok "Server installed in ${INSTALL_DIR}"
    echo
    echo -e "  ${BOLD}Your encryption key (paste into client installer):${NC}"
    echo -e "  ${GREEN}${ENC_KEY}${NC}"
    echo
    echo -e "  ${BOLD}Reminder:${NC} set the NS record for ${CYAN}${DOMAIN}${NC}"
    echo -e "  to point to this server's public IP so DNS resolvers"
    echo -e "  forward tunnel queries here."
    echo
    echo -e "  Check status : ${CYAN}systemctl status masterdns2udp-server${NC}"
    echo -e "  View logs    : ${CYAN}journalctl -u masterdns2udp-server -f${NC}"
    echo

# =============================================================================
# CLIENT
# =============================================================================
else

    banner "Client configuration"

    ask         DOMAIN       "Tunnel domain (must match server, e.g. vpn.example.com)"
    ask         CLIENT_IP    "This machine's public IPv4 (server will send downloads here)"
    ask_optional DL_PORT     "UDP download port (must match server UDP_DOWNLOAD_PORT)"   "5555"
    ask_optional LISTEN_PORT "Local SOCKS5 listen port"                                  "18000"
    ask_optional LOG_LEVEL   "Log level (DEBUG/INFO/WARN/ERROR)"                         "INFO"

    # Encryption key
    banner "Encryption key"
    echo "  Paste the encryption key shown at the end of the server installer."
    echo
    ENC_KEY=""
    while [[ -z $ENC_KEY ]]; do
        read -rp "$(echo -e "${BOLD}Encryption key: ${NC}")" ENC_KEY
        [[ -z $ENC_KEY ]] && warn "Key is required."
    done

    # Resolvers
    banner "DNS resolvers"
    echo "  Enter the Iranian public DNS resolvers the client will send"
    echo "  tunnel queries through (one per line, format IP:PORT)."
    echo "  Press Enter on a blank line when done."
    echo "  Example:  178.22.122.100:53"
    echo
    RESOLVERS=()
    while true; do
        read -rp "$(echo -e "${BOLD}  Resolver (blank to finish): ${NC}")" R
        [[ -z $R ]] && break
        # Basic format check
        if [[ $R =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:[0-9]+$ ]]; then
            RESOLVERS+=("$R")
            ok "  Added: $R"
        else
            warn "  Invalid format — use IP:PORT (e.g. 178.22.122.100:53)"
        fi
    done

    if [[ ${#RESOLVERS[@]} -eq 0 ]]; then
        warn "No resolvers entered. Adding placeholder — edit ${INSTALL_DIR}/client_resolvers.txt before starting."
        RESOLVERS=("0.0.0.0:53  # REPLACE with a real Iranian resolver")
    fi

    # Write client_resolvers.txt
    RESOLVERS_PATH="${INSTALL_DIR}/client_resolvers.txt"
    printf '%s\n' "${RESOLVERS[@]}" > "$RESOLVERS_PATH"
    ok "client_resolvers.txt written → ${RESOLVERS_PATH}"

    # Write client.toml
    CONFIG_PATH="${INSTALL_DIR}/client.toml"
    cat > "$CONFIG_PATH" <<EOF
# MasterDns2Udp — Client Config (generated by installer)

PROTOCOL_TYPE = "SOCKS5"

LISTEN_IP   = "127.0.0.1"
LISTEN_PORT = ${LISTEN_PORT}

UDP_DOWNLOAD_IP   = "${CLIENT_IP}"
UDP_DOWNLOAD_PORT = ${DL_PORT}

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
EOF
    ok "client.toml written → ${CONFIG_PATH}"

    # Binary
    banner "Binary"
    ensure_binary "masterdns2udp-client"

    # Firewall
    banner "Firewall"
    if ask_yn "  Open UDP port ${DL_PORT} in firewall (ufw/firewalld)?"; then
        open_port "$DL_PORT" udp
    fi

    # Systemd
    banner "Systemd service"
    write_systemd_service \
        "masterdns2udp-client" \
        "MasterDns2Udp Client" \
        "${INSTALL_DIR}/masterdns2udp-client" \
        "$CONFIG_PATH"
    enable_and_start "masterdns2udp-client"

    # Summary
    banner "Done"
    echo
    ok "Client installed in ${INSTALL_DIR}"
    echo
    echo -e "  ${BOLD}SOCKS5 proxy:${NC}  ${CYAN}127.0.0.1:${LISTEN_PORT}${NC}"
    echo -e "  Point your browser / app at this address."
    echo
    echo -e "  Check status : ${CYAN}systemctl status masterdns2udp-client${NC}"
    echo -e "  View logs    : ${CYAN}journalctl -u masterdns2udp-client -f${NC}"
    echo -e "  Edit resolvers: ${CYAN}${INSTALL_DIR}/client_resolvers.txt${NC}"
    echo
fi
