# Tailscale

The private option. Bard is reachable from your own devices anywhere in the
world and from nowhere else — no public hostname, no open port, and nobody in
the path who can see your traffic.

The trade: every device that reads must be on your tailnet. That is fine for a
family and wrong for sharing a library with a friend who will not install a VPN.

```
phone (on tailnet)  ──wireguard──▶  bard
                                 100.x.y.z:2273
```

```yaml
trusted_proxies: []
```

**Empty, and that is correct.** Tailscale is not a proxy — it is a network
interface. Requests arrive from the peer's real tailnet address, so Bard can
read it straight off the connection. There is no forwarded header, and nothing
that could legitimately send one.

The exception is `tailscale serve`, below, which *is* a proxy.

## Bard's side

```yaml
data_dir: /var/lib/bard
bind_addr: 0.0.0.0
port: 2273
trusted_proxies: []

tls:
  mode: "off"
```

`bind_addr: 0.0.0.0` listens on the tailnet interface along with everything
else. To serve *only* the tailnet, put this host's tailnet address in
`bind_addr` — then nothing on your LAN can reach it either.

## Setting it up

```
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
```

Then from any other device on the tailnet:

```
http://<hostname>:2273/
```

where `<hostname>` is what the host is called in your tailnet — MagicDNS makes
that work without you tracking addresses.

## Uploads need HTTPS, which plain Tailscale does not give you

Over `http://<hostname>:2273` the browser considers you to be in an insecure
context and withholds `crypto.subtle`, which the upload page needs to hash
chunks. The page will tell you so rather than failing cryptically, but it will
not upload.

`tailscale serve` fixes that by terminating TLS with a real certificate for
your `*.ts.net` name:

```
sudo tailscale serve --bg --https=443 http://127.0.0.1:2273
```

**This changes the `trusted_proxies` answer**, because now there genuinely is a
proxy on the host:

```yaml
trusted_proxies: ["127.0.0.1/32"]
```

Leave it empty with `tailscale serve` running and every device collapses into
one rate-limit bucket. Load `/diagnostics` after switching and it will say so.

With `serve` running you may also want `bind_addr: 127.0.0.1`, so the only way
in is through the TLS front door.

## Why this is the most private option

Cloudflare Tunnel terminates TLS at Cloudflare's edge, so Cloudflare can see
your traffic. A reverse proxy on a public IP puts your login form in front of
the whole internet. Tailscale does neither: the connection is end-to-end
encrypted between your own devices, and an attacker who is not on your tailnet
cannot reach Bard at all.

That also means Phase 2's rate limiting matters less here — not because it
stopped working, but because nobody unauthenticated can get to it.
