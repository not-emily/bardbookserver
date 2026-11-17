# Reverse proxy

For a host that already has a public IP and something terminating TLS on it.
This is the recipe where **you** are responsible for the server being
internet-attackable, so read the last section.

```
phone  ──https──▶  caddy / nginx  ──http──▶  bard
                   (same host)            127.0.0.1:2273
```

```yaml
trusted_proxies: ["127.0.0.1/32"]
```

That is the value when the proxy runs on the same host as Bard, which is the
usual arrangement. **If the proxy is on another machine, use that machine's
address instead** — and only that machine's. The list is "hops I will believe a
forwarded header from", never a range of clients.

Load `/diagnostics` through the proxy when you are done; it reports the address
rate limiting keys on and whether it is right.

## Bard's side

```yaml
data_dir: /var/lib/bard
bind_addr: 127.0.0.1
port: 2273
trusted_proxies: ["127.0.0.1/32"]

tls:
  mode: "off"
```

`bind_addr: 127.0.0.1` matters: if Bard listened on `0.0.0.0` it would be
reachable on the public interface directly, bypassing the proxy and its TLS.

## Caddy

Caddy gets certificates itself, which makes this the shortest correct config of
anything here.

```caddyfile
books.example.com {
	reverse_proxy 127.0.0.1:2273
}
```

Caddy sends `X-Forwarded-For` and `X-Forwarded-Proto` by default, so there is
nothing to add.

## nginx

nginx sends neither header unless told to, and a missing `X-Forwarded-For` is
exactly the silent failure `trusted_proxies` exists to catch — Bard would trust
the hop and then find nothing to read from it.

```nginx
server {
    listen 443 ssl http2;
    server_name books.example.com;

    ssl_certificate     /etc/letsencrypt/live/books.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/books.example.com/privkey.pem;

    # Audiobooks are large and reads are ranged; buffering them helps nobody.
    proxy_buffering off;
    proxy_request_buffering off;
    client_max_body_size 0;

    location / {
        proxy_pass http://127.0.0.1:2273;
        proxy_http_version 1.1;

        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

`client_max_body_size 0` lifts nginx's 1MB default. Without it an upload fails
at the proxy before Bard ever sees it — and uploads are the one thing here that
sends large request bodies rather than receiving them.

## Letting Bard terminate TLS instead

If you would rather not run a proxy at all, Bard can get its own certificates:

```yaml
bind_addr: 0.0.0.0
port: 443
trusted_proxies: []        # nothing is in front, so believe nobody

tls:
  mode: auto
  domains: ["books.example.com"]
  challenge_addr: ":80"
```

`trusted_proxies` is **empty** here, and that is correct: with nothing in front
of Bard, a forwarded header could only have come from the caller, so believing
one would let anybody claim any address.

An ACME challenge is only answered where the CA looks for it — TLS-ALPN-01 on
443, or HTTP-01 on 80 — so `challenge_addr` must be reachable from the internet
on port 80. Both ports are privileged, so the service needs permission to bind
them:

```
sudo systemctl edit bard
```
```ini
[Service]
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
```

Rehearse against staging before the real thing. Let's Encrypt's production
rate limits take a week to recover from, and a typo in a domain name is enough
to hit them:

```yaml
tls:
  acme_directory: "https://acme-staging-v02.api.letsencrypt.org/directory"
```

Your browser will refuse the staging certificate — that is the point of
staging. Remove the line once the log shows a certificate was issued.

## This is the recipe that exposes you

A forwarded port means anyone on the internet can reach your login form. What
makes that survivable is Phase 2's rate limiting, which is why it is keyed on
the *real* client address and why `trusted_proxies` being right is not
optional.

If you do not actually need a public hostname, you do not need this. Use
[Cloudflare Tunnel](cloudflare-tunnel.md), where nothing is exposed, or
[Tailscale](tailscale.md), where nothing is public at all.
