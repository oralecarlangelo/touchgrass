# touchgrass on EC2 (ops notes)

Live deployment serving the UI at <https://infra.ticketnation.ph>.
Installed 2026-09-21 on the shared TicketNation box (`52.74.121.228`).

## Layout on the box

| What              | Where                                             |
| ----------------- | ------------------------------------------------- |
| Binary + DB       | `/home/ubuntu/touchgrass/` (`touchgrass`, `*.db*`) |
| Env (password!)   | `/home/ubuntu/touchgrass/touchgrass.env` (0600)   |
| Password backup   | `/home/ubuntu/touchgrass/.admin-pass` (0600)      |
| systemd unit      | `/etc/systemd/system/touchgrass.service`          |
| nginx site        | `/etc/nginx/sites-available/touchgrass`           |
| TLS cert          | `/etc/letsencrypt/live/infra.ticketnation.ph/`    |
| DNS               | Route53 `ticketnation.ph` zone, A 300s → box IP   |

## Security posture

- Origin binds `127.0.0.1:8080` only; nginx proxies locally.
- TLS via Let's Encrypt (certbot `--nginx`, auto-renewed); HTTP 301s
  to HTTPS; HSTS `max-age=31536000; includeSubDomains`.
- Session cookie: `HttpOnly; Secure; SameSite=Lax`
  (`TOUCHGRASS_COOKIE_SECURE=true` — required behind TLS).
- `/api/auth/login` rate-limited in nginx: 5 req/min, burst 3.
- Single admin password, bcrypt-hashed in memory, env-only. Fetch it
  with `ssh -i ~/.ssh/ticketnation-api.pem ubuntu@52.74.121.228
  'cat ~/touchgrass/.admin-pass'` — never commit or chat it.
- Full model: `docs/threat-model.md`.

## Operating

```bash
ssh -i ~/.ssh/ticketnation-api.pem ubuntu@52.74.121.228
sudo systemctl status touchgrass   # health
sudo journalctl -u touchgrass -f   # logs (stdout JSON)
sudo systemctl restart touchgrass  # restart (reads touchgrass.env)
```

Deploy a new binary (stop first — Linux refuses to overwrite a
running executable, and never `pkill -f` with a pattern that matches
its own remote shell):

```bash
# locally: GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/touchgrass-linux-arm64 ./cmd/touchgrass
ssh ... 'sudo systemctl stop touchgrass'
scp ... /tmp/touchgrass-linux-arm64 ubuntu@52.74.121.228:~/touchgrass/touchgrass
ssh ... 'sudo systemctl start touchgrass'
```

## Notes

- No staging stack exists on the box — validations that assume one
  use adapted prod-safe procedures (see `docs/dogfood-runbook.md` §8).
- No Go/Node toolchains on the box: always ship a cross-compiled
  static binary (pure-Go SQLite, `CGO_ENABLED=0`).
- The DB holds prod log lines + occurrences: `chmod 600`
  `touchgrass.db*`, keep it out of off-box backups (threat model §5).
