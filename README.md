# Marco

**Marco… Polo!** Marco watches the home network for your kids' phones and texts you when one
drops off the Wi-Fi during a schedule you set. That's usually what happens when a kid
turns Wi-Fi off to use cellular data and get around the router's parental controls.

- Family members, each with devices (phone, tablet, laptop, console…) and watch schedules
  like "School nights, Sun–Thu 9 PM – 7 AM"
- Text alerts via [Textbelt](https://textbelt.com), plus an optional JSON webhook (Home Assistant, ntfy, Slack…)
- Reminder texts while a device stays off Wi-Fi, and a "Polo!" text when it comes back
- Optionally **text the kid too** ("Looks like you went offline? This has been logged…"),
  with a default message and an optional custom one per kid
- One-tap **pause** per kid (sleepover, vacation)
- A complete activity log, plus on/off timelines and hours-per-day charts
- Network scan to find devices, with hostnames from your router
- Multiple parent accounts, each choosing whether they get texts
- One Go binary with SQLite built in (no cgo). Runs on a Raspberry Pi, a NAS, Docker or a Mac.

## Quick start

```sh
make build
sudo ./marco            # open http://<this-computer>:8080
```

On first visit you create your parent account. Then:

1. **Family → Add family member.** They start with a "School nights" schedule you can change.
2. **Find devices → Scan now**, then press **Add** next to the kid's phone. To be sure you
   have the right one, on an iPhone check *Settings → Wi-Fi → ⓘ* for its IP address.
3. **Settings → Text alerts:** paste your Textbelt key and press **Send test**.
4. **Parents:** add your partner's account and mobile number.
5. Optional: on a kid's page, turn on **Text them when they go offline** and add their
   number. The kid gets one text per drop, over cellular, since that's what they're on.
   Reminders only go to parents. **Send a test text** shows them exactly what it looks like.

> **Reserve the phone's IP** in your router's DHCP settings (UniFi: *Client → Settings →
> Fixed IP*). Marco also follows a device by MAC address if its IP changes, but a fixed IP
> is the most reliable.

## How detection works

Every check (60 s by default), Marco probes each device three ways at once:

| Probe | Why |
|---|---|
| ICMP ping | Quick, but sleeping phones often ignore it |
| TCP connect to a few ports (62078 = iPhone sync, 80, 443…) | Even a "connection refused" proves the phone is there |
| ARP | The phone's Wi-Fi chip, or the access point on its behalf, answers ARP even while asleep. This is the most reliable signal. |

If any probe gets an answer, the phone is on Wi-Fi. After *N* checks in a row with no
answer (4 by default), the device is marked **Off Wi-Fi**. If that happens during one of
its owner's schedules, every parent with alerts turned on gets a text.

**Run as root (or with `CAP_NET_ADMIN`) on Linux for the best results.** Before each
check, Marco clears the device's ARP entry so a stale cached entry can't make a
disconnected phone look present. Without root, Linux still works because Marco only
trusts `REACHABLE` entries. macOS doesn't expose ARP freshness, so without root it can
take up to about 20 minutes to notice a phone has left. A Raspberry Pi running as root is
the ideal setup.

A phone that's switched off or has a dead battery looks the same as one with Wi-Fi turned off.

## Running it for real

**Docker (Linux host):**

`docker-compose.yml` is set up for [Dokploy](https://dokploy.com): create a Compose app from this repo,
set `TZ` (and optionally `MARCO_PORT`, default 9999) under Environment, then add your domain to the
`proxy` service on port 80.

Host networking is required so the container can see the LAN, so marco can't join Dokploy's network
directly. The small Caddy `proxy` service sits on `dokploy-network` and forwards to marco on the host.
Data lives in Dokploy's persistent `../files/marco-data`.

Outside Dokploy, run `docker network create dokploy-network` once, then `docker compose up -d --build`.

**Raspberry Pi / systemd:**

```sh
make pi                                    # builds dist/marco-linux-arm64
scp dist/marco-linux-arm64 pi:/tmp/marco
ssh pi 'sudo install /tmp/marco /usr/local/bin/marco'
scp deploy/marco.service pi:/tmp/ && ssh pi 'sudo mv /tmp/marco.service /etc/systemd/system/ && sudo systemctl enable --now marco'
```

### Options

| Flag | Env | Default |
|---|---|---|
| `-addr` | `MARCO_ADDR` | `:8080` |
| `-db` | `MARCO_DB` | `marco.db` |

Everything else (check interval, ports, message text, timezone, history retention) is on
the Settings page.

**Forgot your password?**
`MARCO_NEW_PASSWORD='new-password' ./marco -db marco.db -reset-password jamie`

## Webhook payload

```json
{
  "event": "alert",
  "message": "Marco: Emma's iPhone left the home Wi-Fi at 9:42 PM during \"School nights\".",
  "text": "…same as message…",
  "person": "Emma", "device": "iPhone", "ip": "192.168.1.42",
  "schedule": "School nights",
  "since": "2026-09-30T21:42:00-05:00", "at": "2026-09-30T21:46:10-05:00"
}
```

`event` is one of `alert`, `recovered` or `test`.

## Security notes

Marco is meant for your home LAN. Passwords are hashed with bcrypt. Sessions use
HttpOnly, SameSite=Lax cookies, and cross-origin form posts are rejected. Don't expose it
to the internet without a reverse proxy that adds HTTPS.

## Development

```sh
make test
```

- `internal/probe`: presence detection (ICMP/TCP/ARP) and the network scan
- `internal/monitor`: the check loop and the online/offline/alert state machine
- `internal/notify`: Textbelt and webhooks
- `internal/store`: SQLite storage, schedules and history
- `internal/web`: handlers, Tabler templates and ApexCharts
