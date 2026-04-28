# MasterDns2Udp

**MasterDns2Udp** is a fork of [MasterDnsVPN](https://github.com/masterking32/MasterDnsVPN) that implements an **asymmetric tunnel**: upload traffic travels through DNS TXT queries while download traffic flows over a dedicated raw UDP channel. This eliminates the DNS response-size bottleneck and dramatically improves download throughput compared to a symmetric DNS tunnel.

---

## How It Differs from MasterDnsVPN

| Feature | MasterDnsVPN | MasterDns2Udp |
| :--- | :--- | :--- |
| Upload path | DNS queries | DNS queries (unchanged) |
| Download path | DNS responses (TXT records) | **Raw UDP channel** |
| Download bottleneck | DNS response size limit | None — full UDP MTU |
| Extra firewall rule needed | No | Yes — one UDP port for download |
| Session init payload | 10 bytes | 16 bytes (+ client IP + port) |

---

## Architecture Overview

```
Client                         Iranian Resolvers          Server (Iran)
──────                         ─────────────────          ─────────────
App ──► SOCKS5 proxy
         │
         ▼
  DNS TXT queries ──────────────────────────────────────► UDP_PORT (53)
                                                           │
                                                     [session + stream]
                                                           │
  UDP download socket ◄─────────────────────────── UDP_DOWNLOAD_PORT (5555)
         │
         ▼
    App receives data
```

- **Upload**: DNS TXT queries go through Iranian public resolvers, forwarded to the server's authoritative NS.
- **Download**: The server sends encrypted raw UDP packets directly to the client's public IP:port.

---

## Prerequisites

### DNS delegation (server side)

You must delegate a subdomain to your server so DNS resolvers forward tunnel queries to it.

1. Create an `A` record: `ns.example.com → <server-IP>` (DNS only, not proxied)
2. Create an `NS` record: `tunnel.example.com → ns.example.com`

The client puts `tunnel.example.com` in `DOMAINS` and the server puts it in `DOMAIN`.

### Firewall (server side)

Open both UDP ports:

```bash
# ufw
ufw allow 53/udp
ufw allow 5555/udp

# firewalld
firewall-cmd --permanent --add-port=53/udp --add-port=5555/udp
firewall-cmd --reload
```

### Firewall (client side)

Open the download port so the server can reach you:

```bash
ufw allow 5555/udp
```

---

## Quick Installation (Linux)

The interactive installer handles everything: binary download, config generation, firewall rules, and systemd service.

```bash
curl -fsSL https://raw.githubusercontent.com/retro1878/MasterDns2Udp/claude/setup-dns-tunneling-QICmN/install.sh | sudo bash
```

Or download and run locally:

```bash
wget https://raw.githubusercontent.com/retro1878/MasterDns2Udp/claude/setup-dns-tunneling-QICmN/install.sh
sudo bash install.sh
```

Choose **1 for server** or **2 for client** when prompted. The script will ask for the values it needs and set everything up.

---

## Manual Installation

### 1. Download binaries

```bash
mkdir -p /opt/masterdns2udp
cd /opt/masterdns2udp

# Server binary
curl -fsSL https://raw.githubusercontent.com/retro1878/MasterDns2Udp/claude/setup-dns-tunneling-QICmN/masterdns2udp-server -o masterdns2udp-server
chmod +x masterdns2udp-server

# Client binary
curl -fsSL https://raw.githubusercontent.com/retro1878/MasterDns2Udp/claude/setup-dns-tunneling-QICmN/masterdns2udp-client -o masterdns2udp-client
chmod +x masterdns2udp-client
```

### 2. Configuration

Copy and edit the example configs from the repository:

- `server.toml` → server configuration
- `client.toml` → client configuration
- `client_resolvers.example.txt` → rename to `client_resolvers.txt` and add your Iranian DNS resolvers (one `IP:PORT` per line)

**Key values to fill in:**

Server (`server.toml`):
- `DOMAIN` — your tunnel domain, e.g. `["tunnel.example.com"]`
- `ENCRYPTION_KEY_FILE` — path to `encrypt_key.txt` (generate with `openssl rand -base64 32 > encrypt_key.txt`)

Client (`client.toml`):
- `DOMAINS` — must match server `DOMAIN`
- `UDP_DOWNLOAD_IP` — the client's **public IPv4** address (where the server sends download packets)
- `ENCRYPTION_KEY` — contents of the server's `encrypt_key.txt`

### 3. Run

```bash
# Server
./masterdns2udp-server -config server.toml

# Client
./masterdns2udp-client -config client.toml
```

### 4. Systemd

```bash
# Server service
cat > /etc/systemd/system/masterdns2udp-server.service <<'EOF'
[Unit]
Description=MasterDns2Udp Server
After=network.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/opt/masterdns2udp/masterdns2udp-server -config /opt/masterdns2udp/server.toml
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now masterdns2udp-server
```

---

## Build from Source

Requires Go 1.24+.

```bash
git clone https://github.com/retro1878/MasterDns2Udp.git
cd MasterDns2Udp

go build -o masterdns2udp-server ./cmd/server
go build -o masterdns2udp-client ./cmd/client
```

---

## Configuration Reference

See the annotated example files in the repository:

- [`server.toml`](server.toml) — all server parameters with comments
- [`client.toml`](client.toml) — all client parameters with comments
- [`client_resolvers.example.txt`](client_resolvers.example.txt) — resolver list format

### Critical parameters

| Parameter | Where | Notes |
| :--- | :--- | :--- |
| `UDP_DOWNLOAD_PORT` | server + client | Must match on both sides |
| `UDP_DOWNLOAD_IP` | client only | Client's public IPv4 that the server sends data to |
| `DOMAIN` / `DOMAINS` | server + client | Must match exactly |
| `ENCRYPTION_KEY_FILE` | server | Path to shared key file |
| `ENCRYPTION_KEY` | client | Contents of server's key file |

---

## Troubleshooting

**Port 53 already in use** — disable `systemd-resolved` stub:

```bash
echo "DNSStubListener=no" | sudo tee -a /etc/systemd/resolved.conf
sudo systemctl restart systemd-resolved
```

**No download traffic** — check:
1. Client `UDP_DOWNLOAD_IP` is set to the correct public IP
2. `UDP_DOWNLOAD_PORT` matches on both sides
3. Client-side firewall allows that UDP port inbound

**Check service status:**

```bash
systemctl status masterdns2udp-server
journalctl -u masterdns2udp-server -f

systemctl status masterdns2udp-client
journalctl -u masterdns2udp-client -f
```

---

## License

MIT — see `LICENSE` file.

---

## Credits

Based on [MasterDnsVPN](https://github.com/masterking32/MasterDnsVPN) by MasterkinG32.
Asymmetric UDP download channel fork by [retro1878](https://github.com/retro1878).
