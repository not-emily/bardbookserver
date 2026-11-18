# Cloudflare Tunnel

**The recommended way to reach Bard from outside your house.** Nothing is
port-forwarded, your home IP is never published, and TLS is handled for you.

```
phone  ──https──▶  Cloudflare edge  ──outbound tunnel──▶  cloudflared  ──http──▶  bard
                                                           (your host)        127.0.0.1:2273
```

`cloudflared` dials *out* to Cloudflare and keeps that connection open. Nothing
dials in. That is why there is no firewall rule to open and nothing to expose.

```yaml
trusted_proxies: ["127.0.0.1/32"]
```

**That line is the one thing people get wrong, and it fails silently both
ways.** Every request reaches Bard from `127.0.0.1`, because the last hop is
`cloudflared` on the same host. Leave the list empty and Bard has no reason to
believe the forwarded header, so it keys rate limiting on `127.0.0.1` — your
whole family and anyone attacking you land in one bucket, and one person
fumbling a password locks out everybody. Trust too much and a caller can set
`X-Forwarded-For` to anything and walk past login rate limiting.

Load `/diagnostics` **through the tunnel** once you are done. It reports the
address rate limiting actually keys on, and says plainly whether it is right.

## Bard's side

`tls.mode` stays `off`. Cloudflare terminates TLS at its edge, and the hop from
`cloudflared` to Bard is a loopback connection on the same machine.

**`tls: auto` cannot work here**, and not because of a setting: an ACME
challenge needs an inbound path to answer on, and the entire point of a tunnel
is that there isn't one. Do not set it.

`/etc/bard/bard.yml`:

```yaml
data_dir: /var/lib/bard
bind_addr: 127.0.0.1     # only cloudflared needs to reach it
port: 2273
trusted_proxies: ["127.0.0.1/32"]

tls:
  mode: "off"
```

Binding to `127.0.0.1` rather than `0.0.0.0` means that even if the tunnel
stops, nothing on your LAN is serving your library.

## Cloudflare's side

You need a domain on Cloudflare. The dashboard route needs no local config
file, no browser on the server, and no `cert.pem`.

1. Install `cloudflared` — [Cloudflare's downloads][dl].
2. In the [Zero Trust dashboard][zt], go to **Networks → Tunnels → Create a
   tunnel**, pick **Cloudflared**, and name it. It shows you a single install
   command containing a token.
3. Run that command on the host. It installs and starts a service:
   ```
   sudo cloudflared service install <TOKEN>
   ```
4. Still in the dashboard, give the tunnel a route: **Add a route → Published
   application**. The other three options are Zero Trust private networking and
   need the WARP client on every device, which is not what you want here.
   - **Subdomain/domain**: whatever you want, e.g. `books.example.com`
   - **Service type**: `HTTP`
   - **URL**: `localhost:2273`

   Cloudflare used to call this a **Public Hostname**, and older guides still
   do. If the wording has moved again, the one you want is whichever says it
   publishes to the internet.

DNS is created for you. `https://books.example.com` now reaches Bard.

**The token is a capability over your Cloudflare account, not a Bard
credential.** Bard never sees it, never stores it and never speaks to
Cloudflare — which is deliberate, and why Bard has no `cloudflared` wrapper
command. Treat it like a password: it belongs to `cloudflared`.

[dl]: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/
[zt]: https://one.dash.cloudflare.com/

## Two caveats worth knowing before you commit

**Cloudflare's self-serve terms discourage proxying large volumes of
non-HTML content.** Streaming an audiobook library through a free tunnel is
squarely the thing section 2.8 of their terms is about. Nobody is likely to
notice a family-sized library, but you should know the rule exists rather than
discover it.

**Cloudflare terminates TLS, so Cloudflare can see your traffic.** The
connection is encrypted phone-to-edge and edge-to-host, not end-to-end. If that
is not acceptable, use [Tailscale](tailscale.md) instead, where nobody but you
is in the path.

## Checking it worked

```
curl https://books.example.com/healthz
```

Then, in a browser **through the tunnel**, open `/diagnostics`. You want:

> Client addresses are resolving correctly.

If instead it says something is forwarding and Bard is ignoring it, your
`trusted_proxies` is empty — the page names the value to set.

## Uploads need HTTPS, and this gives it to you

The upload page hashes each chunk with `crypto.subtle`, which browsers withhold
outside a secure context. Through the tunnel you are on HTTPS, so it works. On
plain HTTP over a LAN address it will not, and the page says so rather than
failing cryptically.
