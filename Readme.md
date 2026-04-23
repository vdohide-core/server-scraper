# Server Scraper

Web scraping API service สำหรับดึง metadata และ stream URL จากเว็บไซต์ต่างๆ สำหรับ [VDOHide](https://vdohide.com)

## Features

- **MissAV** — scrape metadata, m3u8 URL (missav.ai, missav.ws)
- **XVideos** — scrape metadata, HLS/MP4 URL (xvideos.com)
- **PornHub** — scrape metadata, HLS/MP4 URL (pornhub.com)
- **Google Drive** — ดึง metadata ผ่าน OAuth2 API v3
- **Direct URL** — validate และ inspect ไฟล์ mp4/mkv/m3u8 โดยตรง
- **Anti-Bot** — fallback ไปใช้ Headless Chrome (go-rod + stealth) เมื่อโดน 403
- **CORS** — รองรับ cross-origin requests

## Requirements

- **Chromium** หรือ **Google Chrome** (สำหรับ Cloudflare bypass)
- **MongoDB** (optional — สำหรับ Google Drive OAuth)

---

## Installation (Linux Server)

### One-line install

```bash
curl -fsSL https://raw.githubusercontent.com/vdohide-core/server-scraper/main/install.sh | sudo -E bash
```

### Options

| Option | Default | คำอธิบาย |
|---|---|---|
| `-p, --port` | `8081` | HTTP port |
| `--mongodb-uri` | `""` | MongoDB connection string (สำหรับ Google Drive OAuth) |
| `--uninstall` | — | ถอนการติดตั้ง |

### Examples

```bash
# Install with custom port + MongoDB
curl -fsSL https://raw.githubusercontent.com/vdohide-core/server-scraper/main/install.sh | sudo -E bash -s -- \
    --port 8081 \
    --mongodb-uri "mongodb+srv://user:pass@cluster.mongodb.net/platform"

# Uninstall
curl -fsSL https://raw.githubusercontent.com/vdohide-core/server-scraper/main/install.sh | sudo bash -s -- --uninstall
```

### After install

```bash
# ดู logs
journalctl -u server-scraper -f

# Restart
systemctl restart server-scraper

# Status
systemctl status server-scraper
```

---

## Download Latest Release

```bash
# Linux amd64
curl -L https://github.com/vdohide-core/server-scraper/releases/latest/download/linux -o server-scraper
chmod +x server-scraper

# Linux ARM64
curl -L https://github.com/vdohide-core/server-scraper/releases/latest/download/linux-arm64 -o server-scraper
chmod +x server-scraper
```

---

## API Endpoints

### `GET /health`
```json
{ "status": "ok", "service": "server-scraper" }
```

### `GET /parsers`
```json
{ "parsers": [...], "count": 5 }
```

### `GET /scraper?url=<URL>`
### `POST /scraper` `{ "url": "<URL>" }`

```json
{
  "success": true,
  "parser": "MissAV Parser",
  "url": "https://missav.ai/en/abcd-123",
  "data": {
    "title": "...",
    "poster": "https://...",
    "m3u8Url": "https://...",
    "duration": 3600,
    "actresses": [{ "id": "...", "value": "..." }]
  },
  "timestamp": "2026-01-01T00:00:00Z"
}
```

---

## Configuration (.env)

```env
# HTTP port
HTTP_PORT=8081

# HTTP timeout in seconds (default: 30)
HTTP_TIMEOUT=30

# MongoDB URI (optional — สำหรับ Google Drive OAuth)
MONGODB_URI=mongodb+srv://user:pass@cluster.mongodb.net/platform
```

---

## Development

```bash
# Clone
git clone https://github.com/vdohide-core/server-scraper.git
cd server-scraper

# สร้าง .env
cp .env.example .env

# Run
go run ./cmd

# Build all platforms
./build.bat
```

---

## Release

```bash
git tag v1.0.0
git push origin v1.0.0
```

GitHub Actions จะ build และ release อัตโนมัติพร้อม:
- `linux` — Linux amd64 binary
- `linux-arm64` — Linux ARM64 binary
- `install.sh` — Installation script

---

## Parsers

| Parser | Domains | Method |
|---|---|---|
| MissAV | missav.ai, missav.ws | HTML scraping |
| XVideos | xvideos.com | HTML scraping |
| PornHub | pornhub.com | HTML scraping |
| Google Drive | drive.google.com | OAuth2 API v3 |
| Direct URL | *.mp4, *.m3u8, ... | HEAD request |

> Google Drive ต้องการ `oauths` collection ใน MongoDB สำหรับ OAuth credentials
