# MasterDns2Udp

| [English Version](README.MD) | [نسخه فارسی](README_FA.MD) |

**MasterDns2Udp** یک فورک از [MasterDnsVPN](https://github.com/masterking32/MasterDnsVPN) است که یک تونل **نامتقارن** پیاده‌سازی می‌کند:
- **آپلود**: از طریق کوئری‌های DNS TXT (مانند MasterDnsVPN)
- **دانلود**: از طریق یک کانال UDP اختصاصی مستقیم

این تغییر، محدودیت اندازهٔ پاسخ DNS را حذف کرده و سرعت دانلود را به‌طور چشمگیری افزایش می‌دهد.

---

## تفاوت با MasterDnsVPN

| ویژگی | MasterDnsVPN | MasterDns2Udp |
| :--- | :--- | :--- |
| مسیر آپلود | DNS query | DNS query (بدون تغییر) |
| مسیر دانلود | DNS response (TXT) | **UDP مستقیم** |
| گلوگاه دانلود | محدودیت اندازهٔ پاسخ DNS | ندارد |
| نیاز به پورت اضافه | خیر | یک پورت UDP برای دانلود |

---

## معماری

```
کلاینت (خارج از ایران)        رزولورهای ایرانی         سرور (ایران)
──────────────────────        ─────────────────         ────────────
برنامه ──► SOCKS5 proxy
              │
              ▼
    DNS TXT query ─────────────────────────────────► پورت 53 UDP
                                                      │
                                                 [پردازش session]
                                                      │
    دریافت پاسخ UDP ◄────────────────────── UDP_DOWNLOAD_PORT (5555)
              │
              ▼
         برنامه داده دریافت می‌کند
```

---

## پیش‌نیازها

### تنظیمات DNS (سمت سرور)

باید یک subdomain به سرور شما delegate شود تا رزولورها کوئری‌های تونل را به سرور ارسال کنند:

1. یک رکورد `A` بسازید: `ns.example.com → <IP-سرور>` (بدون پروکسی)
2. یک رکورد `NS` بسازید: `tunnel.example.com → ns.example.com`

کلاینت `tunnel.example.com` را در `DOMAINS` قرار می‌دهد و سرور در `DOMAIN`.

### فایروال (سمت سرور)

```bash
# ufw
ufw allow 53/udp
ufw allow 5555/udp

# firewalld
firewall-cmd --permanent --add-port=53/udp --add-port=5555/udp
firewall-cmd --reload
```

### فایروال (سمت کلاینت)

```bash
ufw allow 5555/udp
```

---

## نصب سریع (لینوکس)

اسکریپت نصب به‌صورت تعاملی همه‌چیز را انجام می‌دهد:

```bash
curl -fsSL https://raw.githubusercontent.com/retro1878/MasterDns2Udp/claude/setup-dns-tunneling-QICmN/install.sh | sudo bash
```

یا دانلود و اجرای محلی:

```bash
wget https://raw.githubusercontent.com/retro1878/MasterDns2Udp/claude/setup-dns-tunneling-QICmN/install.sh
sudo bash install.sh
```

در اولین سؤال **1 برای سرور** یا **2 برای کلاینت** وارد کنید.

---

## نصب دستی

### 1. دانلود باینری‌ها

```bash
mkdir -p /opt/masterdns2udp
cd /opt/masterdns2udp

curl -fsSL https://raw.githubusercontent.com/retro1878/MasterDns2Udp/claude/setup-dns-tunneling-QICmN/masterdns2udp-server -o masterdns2udp-server
chmod +x masterdns2udp-server

curl -fsSL https://raw.githubusercontent.com/retro1878/MasterDns2Udp/claude/setup-dns-tunneling-QICmN/masterdns2udp-client -o masterdns2udp-client
chmod +x masterdns2udp-client
```

### 2. پیکربندی

فایل‌های نمونه را از مخزن دریافت و ویرایش کنید:

- `server.toml` ← پیکربندی سرور
- `client.toml` ← پیکربندی کلاینت
- `client_resolvers.example.txt` ← کپی به `client_resolvers.txt` و افزودن رزولورهای ایرانی (یک `IP:PORT` در هر خط)

**مقادیر کلیدی:**

سرور (`server.toml`):
- `DOMAIN` — دامنهٔ تونل، مثال: `["tunnel.example.com"]`
- `ENCRYPTION_KEY_FILE` — مسیر فایل کلید (بسازید: `openssl rand -base64 32 > encrypt_key.txt`)

کلاینت (`client.toml`):
- `DOMAINS` — باید با `DOMAIN` سرور یکسان باشد
- `UDP_DOWNLOAD_IP` — **IP عمومی کلاینت** (سرور بسته‌های دانلود را به اینجا ارسال می‌کند)
- `ENCRYPTION_KEY` — محتوای فایل `encrypt_key.txt` سرور

### 3. اجرا

```bash
./masterdns2udp-server -config server.toml
./masterdns2udp-client -config client.toml
```

---

## بیلد از سورس

نیاز به Go نسخه 1.24 یا بالاتر.

```bash
git clone https://github.com/retro1878/MasterDns2Udp.git
cd MasterDns2Udp

go build -o masterdns2udp-server ./cmd/server
go build -o masterdns2udp-client ./cmd/client
```

---

## عیب‌یابی

**پورت 53 در حال استفاده است** — `systemd-resolved` را غیرفعال کنید:

```bash
echo "DNSStubListener=no" | sudo tee -a /etc/systemd/resolved.conf
sudo systemctl restart systemd-resolved
```

**دانلود کار نمی‌کند** — بررسی کنید:
1. `UDP_DOWNLOAD_IP` در کلاینت برابر IP عمومی واقعی کلاینت باشد
2. `UDP_DOWNLOAD_PORT` در سرور و کلاینت یکسان باشد
3. فایروال کلاینت پورت UDP را باز کرده باشد

**وضعیت سرویس:**

```bash
systemctl status masterdns2udp-server
journalctl -u masterdns2udp-server -f

systemctl status masterdns2udp-client
journalctl -u masterdns2udp-client -f
```

---

## لایسنس

MIT — فایل `LICENSE` را ببینید.

---

## اعتبارات

بر پایهٔ [MasterDnsVPN](https://github.com/masterking32/MasterDnsVPN) نوشتهٔ MasterkinG32.
فورک کانال دانلود UDP نامتقارن توسط [retro1878](https://github.com/retro1878).
