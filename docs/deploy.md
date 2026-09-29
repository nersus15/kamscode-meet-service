# Deploy kamscode meet

Panduan deploy ke homelab (Docker Swarm + HAProxy + Cloudflare Tunnel, internet di belakang CGNAT)
atau ke server ber-IP publik. Satu kode, dua cara deploy — dipilih lewat env.

## Arsitektur

```text
                       ┌──────────── satu domain: https://meet.domain-anda.com ────────────┐
Browser ──HTTPS/WSS──▶ Cloudflare ──Tunnel──▶ HAProxy ─┬─ /        ──▶ web  (nginx, Vue)
                                                        ├─ /api/... ──▶ api  (Go, Postgres)
                                                        └─ /ws      ──▶ sfu  (Go, Pion)  ◀── 1 replika
Browser ──media UDP──▶ (opsi A) langsung ke SFU  /  (opsi B) Cloudflare TURN ◀── SFU
```

| Service | Replika | State | Catatan |
|---|---|---|---|
| web | ≥1 | tidak ada | file statis + header keamanan (CSP) |
| api | ≥1 | Postgres | migrasi otomatis saat start, aman untuk banyak replika (advisory lock) |
| sfu | **tepat 1** | room di memori | peserta satu room wajib di SFU yang sama; tidak butuh database |

## Memilih opsi media

WebRTC media berjalan di **UDP langsung** antara browser dan SFU — tidak bisa lewat Cloudflare Tunnel.

| | Opsi A: SFU ber-IP publik | Opsi B: semua di homelab (CGNAT) |
|---|---|---|
| Kapan | SFU di VPS / internet dengan port forwarding | Internet tanpa IP publik (mis. modem seluler) |
| Env SFU | `SFU_UDP_PORT=50000`, `SFU_PUBLIC_IPS=<ip publik>` | kosongkan keduanya |
| ICE | `ICE_URLS=stun:stun.cloudflare.com:3478` | `CF_TURN_KEY_ID`, `CF_TURN_API_TOKEN` |
| Firewall | buka UDP 50000 ke SFU | tidak perlu membuka port apa pun |
| Bandwidth media | dari server SFU | lewat internet homelab (perhatikan kuota & upload) |

**Cloudflare Realtime TURN** (opsi B): gratis 1.000 GB/bulan, lalu $0.05/GB. Buat kunci di
dasbor Cloudflare → Realtime → TURN Server → *Create*, salin **Key ID** dan **API Token**.
api memberi browser kredensial sementara; SFU memakai TURN yang sama agar selalu punya relay.

## Langkah

### 1. Kunci join token
```bash
make genkey
```
`JOIN_TOKEN_PRIVATE_KEY` hanya untuk **api**, `JOIN_TOKEN_PUBLIC_KEY` untuk **sfu**.

### 2. Env produksi
Salin `infra/prod.env.example` → `infra/prod.env` (di-ignore git) lalu isi. Wajib diperhatikan:

| Env | Nilai |
|---|---|
| `SFU_PUBLIC_URL` | `wss://meet.domain-anda.com/ws` |
| `COOKIE_SECURE` | `true` (situs diakses lewat HTTPS Cloudflare) |
| `SFU_ALLOWED_ORIGINS` | `meet.domain-anda.com` |
| `CLIENT_IP_HEADER` | `CF-Connecting-IP` (rate limit login memakai IP asli pengunjung) |
| `DATABASE_URL` | Postgres Anda; database `kamscode_meet` harus sudah dibuat, tabel dibuat otomatis |

### 3. Build & kirim image ke registry
Swarm multi-node menarik image dari registry — image yang hanya di-build lokal tidak ada di node lain.
```bash
make docker-build
```
Lalu beri tag dan push ke registry Anda (mis. registry privat di Swarm, atau GHCR), dan ubah
`image:` di `infra/stack.yml` sesuai nama tersebut:
```bash
docker tag kamscode-meet-api:latest REGISTRY/kamscode-meet-api:latest && docker push REGISTRY/kamscode-meet-api:latest
```
(ulangi untuk `-sfu` dan `-web`).

### 4. Deploy stack
Dari folder `infra/` (agar `env_file: prod.env` ditemukan):
```bash
docker stack deploy -c stack.yml meet
```
Opsi A: aktifkan blok port UDP `mode: host` dan `placement` di `stack.yml`, lalu beri label node:
`docker node update --label-add sfu=true <node>`.

### 5. HAProxy
Pakai `infra/haproxy.cfg.example` sebagai dasar. Yang penting:
- `timeout tunnel 1h` — tanpa ini WebSocket diputus setelah 50 detik diam.
- `/ws` → sfu, `/api/` → api, sisanya → web, **host yang sama**.
- Health check: web `GET /`, api `GET /api/healthz`, sfu `GET /healthz`.

### 6. Cloudflare Tunnel
Public hostname `meet.domain-anda.com` → `http://<ip-haproxy>:80`. WebSocket didukung otomatis.

## Pemeriksaan setelah deploy
1. `https://meet.domain-anda.com/api/healthz` → `ok`.
2. Daftar akun, buat room, join dari dua jaringan berbeda (mis. WiFi + data seluler).
3. Panel debug (ikon terminal) → `connectionState` publisher & subscriber `connected`.
4. Opsi B: di `chrome://webrtc-internals`, candidate-pair terpilih bertipe `relay` (lewat TURN).
5. Log SFU (`LOG_LEVEL=debug`) tidak pernah berisi teks chat — hanya `chat relayed … envelope_sample=<acak>`.

## Keamanan yang sudah aktif
- Join token Ed25519, umur 60 detik, sekali pakai; SFU hanya memegang public key.
- Sesi login: cookie httpOnly + SameSite=Lax + Secure; token disimpan sebagai hash.
- Rate limit login/register: 20/menit per IP, 5/menit per email (per replika api).
- CSRF: request yang mengubah data wajib `Content-Type: application/json`.
- CSP ketat di web: hanya skrip dari domain sendiri.
- Chat E2EE: server hanya meneruskan ciphertext.
- Ping WebSocket tiap 25 detik (menjaga koneksi melewati timeout Cloudflare/HAProxy).

## Batasan yang diketahui
- SFU tunggal: jika container SFU restart, semua peserta reconnect otomatis (room dibangun ulang).
- Menghapus room tidak mengeluarkan peserta yang sedang di dalam; hanya menolak join baru.
- Rate limit dan penanda token sekali pakai disimpan di memori per instance.
