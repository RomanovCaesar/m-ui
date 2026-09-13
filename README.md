[English](/README.md) | [中文](/README.zh_CN.md)

<p align="center">
  <img src="./media/m-ui_logo.png" alt="m-ui logo" width="420">
</p>

# m-ui

[![Release](https://img.shields.io/github/v/release/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/releases)
[![Build](https://img.shields.io/github/actions/workflow/status/RomanovCaesar/m-ui/release.yml.svg)](https://github.com/RomanovCaesar/m-ui/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/blob/main/go.mod)
[![Downloads](https://img.shields.io/github/downloads/RomanovCaesar/m-ui/total.svg)](https://github.com/RomanovCaesar/m-ui/releases/latest)
[![License](https://img.shields.io/github/license/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/blob/main/LICENSE)

A server management panel powered by Mihomo. Its interface follows the information density and workflow of 3x-ui 2.9.3 while using Mihomo's native YAML configuration model.

> [!IMPORTANT]
> Use this project only on servers and networks that you own or are authorized to administer. Follow local law, upstream provider terms, and the policies of the services you access.

For complete usage and configuration documentation, visit the [m-ui Wiki](https://github.com/RomanovCaesar/m-ui/wiki).

## Linux installation

Run as `root` on a Linux system using systemd or OpenRC:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

When no values are supplied, the installer selects an unused five-digit panel port and generates a 16-character panel path, a 14-character username, and a 16-character password. Every generated string contains uppercase letters, lowercase letters, and digits. Explicit ports may range from `1` to `65535`; explicit paths may use URL-safe segments or `/` for the root path. The final access URL and generated credentials are printed once when installation completes.

Values can also be supplied explicitly:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

m-ui is installed under `/usr/local/m-ui`, with persistent state under `/usr/local/m-ui/data`. Running the installer again upgrades m-ui while preserving existing settings. The installer also downloads a Mihomo core for the current architecture. An existing core is preserved unless `--update-mihomo` is used; `--skip-mihomo` skips core installation.

GitHub Releases contain Linux assets named `m-ui-linux-<architecture>.tar.gz` for `386`, `amd64`, `arm64`, `armv5`, `armv6`, `armv7`, and `s390x`. Each archive contains either the corresponding `m-ui-linux-<architecture>` executable or `m-ui`. Windows amd64 is published as `m-ui-windows-amd64.zip` with `m-ui.exe` inside.

## Docker installation

The Docker image targets Linux amd64 and contains both m-ui and a pinned, SHA-256-verified Mihomo core. The supplied Compose file uses host networking so new inbound ports configured in the panel work immediately without adding a Docker port mapping for every inbound. Run it on a Linux Docker host:

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

If `MUI_PASSWORD` and `MUI_PATH` are empty, the first start generates both securely and prints the URL, username, and password once in the container log. Set them in `.env` before the first start if explicit values are preferred. Initialization variables are ignored after `state.json` exists; subsequent settings should be changed in the panel.

Tagged releases publish `ghcr.io/romanovcaesar/m-ui:<tag>` and stable tags also update `latest`. Once the GHCR package is public, use the published image instead of building locally:

```bash
docker compose pull
docker compose up -d
```

Persistent state and the panel-updatable Mihomo executable are stored in the `m-ui-data` and `m-ui-core` volumes. Normal upgrades keep both volumes. Do not run `docker compose down -v` unless all panel state, subscription tokens, Multi-control identity, and the installed core should be deleted.

The Compose file uses `network_mode: host`, so the panel and every enabled inbound listen directly on the host ports selected in m-ui. The default panel port is `2053`; the default mixed inbound is `12080` TCP/UDP. Firewall rules still need to permit every port that should be reachable. Host networking is intended for Linux servers and may behave differently in Docker Desktop.

TUN inbounds additionally need `/dev/net/tun` and the `NET_ADMIN` capability. Uncomment the prepared `cap_add` and `devices` sections in `docker-compose.yml` only when TUN is used. Ordinary proxy inbounds and outbounds do not need those permissions.

Useful commands:

```bash
docker compose logs -f m-ui
docker compose restart m-ui
docker compose run --rm m-ui version
docker compose run --rm m-ui settings --data-dir /opt/m-ui/data
```

## TLS certificates

The first interactive installation offers:

* Let's Encrypt domain certificates
* Let's Encrypt short-lived IPv4 certificates
* An existing certificate and private key
* The option to skip TLS setup

Domain and IP certificates use `/root/.acme.sh` with standalone HTTP-01, are installed under `/root/cert/`, and are configured as the panel certificate. The renewal reload command restarts m-ui. Non-interactive installation supports `--ssl-domain DOMAIN`, `--ssl-ip IP`, and `--cert FILE --key FILE`. HTTP-01 normally requires public TCP port 80.

After installation, run:

```bash
m-ui ssl
```

The certificate-management menu can issue domain and IP certificates, use the Cloudflare DNS API for domain and wildcard certificates when port 80 is unavailable, force renewal, revoke certificates, list installed certificates, and apply an existing certificate pair to the panel. Cloudflare mode accepts a scoped API Token or a Global API Key plus account email; secret input is not echoed.

## Management commands

```text
m-ui start
m-ui stop
m-ui restart
m-ui status
m-ui logs
m-ui settings
m-ui configure
m-ui update
m-ui update-menu
m-ui ssl
m-ui set-port
m-ui reset-credentials
m-ui reset-path
m-ui clear-logs
m-ui uninstall
```

## Features

### Panel and Mihomo

* 3x-ui-inspired login page, sidebar, dashboard, traffic statistics, and responsive tables
* Native Mihomo YAML generation and validation
* Start, stop, restart, and monitor Mihomo
* Runtime logs, controller state, and active connections
* System CPU, memory, disk, uptime, and traffic information
* Raw configuration preview, copy, and download
* Panel-state backup and restore
* Mihomo version download and switching from official MetaCubeX Releases
* Official GeoIP, GeoSite, and MetaDB updates
* Configurable core path, controller address/secret, mixed port, mode, logging, panel TLS, and credentials

### Inbounds and clients

* Mihomo `mixed`, `socks`, `http`, `shadowsocks`, `snell`, `vmess`, `vless`, `trojan`, `hysteria2`, `tuic`, `anytls`, `mieru`, `sudoku`, `shadowquic`, `trusttunnel`, and `hysteria2-realm` listeners
* Protocol-aware TLS, Reality, ShadowTLS, RestTLS, TLSMirror, JLS, Trojan SS, XHTTP, mKCP, Mekya, and obfuscation settings
* Per-client credentials, traffic quotas, expiry times, traffic-reset schedules, and online information
* Client share links, QR codes, and subscription tokens where the protocol supports them
* Import and export of individual inbounds
* Bulk traffic reset and depleted-client cleanup

Mixed, HTTP, and SOCKS use Mihomo's native username/password list. A Mihomo Shadowsocks listener has only one core password: additional SS client entries can be retained as share-link/panel metadata, but Mihomo cannot enforce separate passwords for the same listener. Snell and other single-secret listeners similarly expose only one meaningful client identity.

### Subscriptions

* Public browser subscription page with usage, quota, expiry, and node information
* Base64 and native Mihomo/Clash subscriptions
* Sing-box, V2Box, V2RayNG, V2RayTun, Npv Tunnel, HApp, Shadowrocket, Streisand, Surge, Surge Mac, Surfboard, Loon, Quantumult X, Stash, and Egern conversions
* Per-Username 16-character subscription tokens
* Optional dedicated subscription service port; an empty value falls back to the panel port
* Independently generated normal and cross-panel subscription paths

Disabled client conversions return 404 and are hidden from the browser subscription page. The bare subscription URL opens that page in a browser and returns a Base64 list to compatible non-browser clients; append `/clash` for native Mihomo YAML.

### Mihomo Settings

The **Mihomo Settings** page contains **Basics**, **Outbounds**, and **Routing Rules**.

Basics is the first tab and initially expands only General. It manages routing mode, direct IP version, IPv6, concurrent TCP dialing, unified delay, outbound test URL, sampling/save intervals, outbound statistics, logging, and basic routing.

Basic Routing applies rules in this order:

1. Blocked IPs and domains
2. IPv4 Routing
3. WARP Routing
4. Custom Routing Rules

m-ui automatically creates the direct policies needed by these options. Reset restores only the Basics draft and does not remove outbounds, routing rules, or historical traffic. Mihomo does not provide Xray's BitTorrent protocol matcher, so that control remains disabled.

Outbounds manage Mihomo proxy nodes and policy groups. The editor supports both Form and native YAML modes. YAML may contain one node, an array, or `proxies`/`proxy-groups`; native extension fields are preserved. A supported sharing link can be converted locally into YAML using the button beside the Link field. Conversion does not fetch remote subscription URLs. Apply the result to the draft, click Save, and restart Mihomo.

Routing Rules are evaluated from top to bottom and may target a proxy, policy group, or Mihomo builtin. Basic Routing and custom rules take effect in Rule mode. An inbound with an explicit `proxy` follows Mihomo's inbound outbound-precedence behavior.

### Cloudflare WARP

The WARP buttons in Outbounds and Basics open the same account window. **Create** accepts Cloudflare's WARP terms and registers a device. The WireGuard private key is generated locally; only the public key is sent to Cloudflare. The account is stored in `data/state.json` and is included in normal panel backups.

**Add Outbound** turns the account endpoint, tunnel addresses, peer public key, and client ID into a Mihomo `wireguard` outbound. WARP Routing then becomes a domain list supporting normal domains, `full:`, `keyword:`, and `geosite:` entries. Save and restart Mihomo to apply the draft. This does not modify the operating system's default route and does not require the system WARP client.

**More Information** refreshes Cloudflare device data. A WARP+ License Key can be applied through the existing device account. **Reset Outbound** rebuilds the node while preserving its name and references. **Delete** removes the local account record and schedules removal of the WARP outbound and Basics rules; it does not unregister the device at Cloudflare. Remove policy-group and rule references before deleting an outbound.

### Multi-control peer network

Panel Settings → Multi-control connects multiple m-ui panels as equal peers. There is no master or controlled node. Any panel can generate or accept a 16–32 character lowercase alphanumeric pairing token; generated tokens remain 16 characters. Other panels join by entering its IP/domain, panel port, protocol, and token.

Each installation creates an independent Ed25519 identity in `data/multi-control.json`. Pairing tokens are used only for initial mutual authentication. After pairing, nodes exchange signed identity heartbeats and signed peer addresses. New members discover the existing network and attempt direct connections, so an introducing panel going offline does not prevent other mutually reachable peers from communicating. Offline members remain saved and are retried with backoff; one panel can save up to 128 peers.

Configure a Local Node address that other servers can actually reach, for example `https://panel-a.example.com:2053`. HTTPS endpoints must use a hostname covered by the certificate; certificate verification is never bypassed. NAT, cloud security groups, and firewalls must permit direct panel-to-panel traffic. m-ui currently provides no NAT traversal or relay.

The hidden panel path is not part of peer handshakes. Peer endpoints are fixed on the panel port:

```text
/_m-ui/peer/v1
/_m-ui/peer/inbound/v1
```

A dedicated subscription port does not expose those paths. Pairing tokens and private keys are not returned by the ordinary state API or propagated through the mesh. Disconnecting a peer is a local block rather than a global removal; allowing rediscovery lets a common online member introduce it again.

`multi-control.json` is intentionally omitted from normal portable panel backups so restoring a backup on another server cannot clone a node identity. Securely migrate that file only when moving the same server; do not copy it when creating a new peer.

### Sync Inbound

Open **Inbounds → General Actions → Sync Inbound**. Other online panels appear on the left and are selected by default. The right-hand matrix maps local Username rows to Inbound columns: selected cells are green, unselected cells are white, and combinations without an actual client are disabled in gray. Select entire panels, Username rows, Inbound columns, or individual cells.

After confirmation, m-ui sends the selected inbound/client data directly to the selected peers and displays per-panel progress and results. Closing the window does not cancel a submitted job. A sync may include up to 256 inbounds, 4,096 clients, and 128 targets with a 4 MiB configuration payload; up to four peers are processed concurrently. The latest 20 in-memory job records are retained until the panel restarts.

Targets identify a synchronized copy by the sender identity and source inbound ID. Repeating a sync updates selected clients and common inbound configuration while preserving unrelated target clients, traffic counters, online records, and subscription tokens. Conflicting local names or ports are reported rather than overwritten. A successful result means the target saved the inbound and generated YAML; restart Mihomo on the target to apply it.

The transfer authenticates temporary X25519 keys with the trusted peer identities and encrypts inbound data/results using AES-GCM. Referenced certificates, private keys, and client CA files are read on the source and transferred as PEM content. Machine-specific values such as listen IPs, masquerade targets, and outbound names still require a compatible target environment.

### Cross-panel subscription aggregation

Panel Settings → Subscription provides a `Cross Panel Subscription Path` independent of the normal path. On first startup or migration, m-ui generates `/isub` plus a 16-character lowercase alphanumeric token. Manual values must use the same format.

The public addresses are:

```text
https://example.com:port/<cross-panel-path>/<username-token>
https://example.com:port/<cross-panel-path>/<username-token>/clash
```

Open **Inbounds → General Actions → Export All Subscriptions (Cross Panel)** and click **Pull all inbound information**. m-ui pulls inbound and Username-token data from all paired panels, shows task progress, and caches successful remote data in `data/cross-panel-subscriptions.json`. Local inbounds are always read live. A temporary remote error preserves the last successful cache; **Clear remote cache** removes all non-local cached sources.

The window exports bare browser subscription URLs for every aggregated Username. Nodes are sorted by Inbound Name. Remote node/token data travels only through the signed, encrypted peer channel, and a Username that exists only on a remote panel can still appear through its cached token.

## Development

Run from source on Windows:

```powershell
.\scripts\run.ps1
```

The development server opens at `http://127.0.0.1:2053` with the source default account `admin` / `admin`. Change these immediately if the development server is reachable by another machine. Runtime state is stored in `data/`.

Build Windows and Linux amd64 binaries:

```powershell
.\scripts\build.ps1
```

Build all Linux Release archives:

```powershell
.\scripts\build-release.ps1
```

Build scripts prefer `MUI_VERSION`, otherwise use an exact Git tag on the current commit, then fall back to `dev-<commit>`. The value is injected with `-X main.version=...`, so `m-ui version`, panel state, log messages, backup manifests, and the HTTP User-Agent report the actual build version.

Run validation directly with:

```bash
go test ./...
go vet ./...
```

## Source layout

```text
main.go                    Program entry point
internal/app/              Panel backend and Go tests
internal/mihomoconvert/    Mihomo sharing-link converter
web/                       Embedded frontend pages, styles, and scripts
tests/                     Browser-side regression scripts
scripts/                   Run, build, Release, and Linux management scripts
deploy/                    systemd and OpenRC service definitions
install.sh                 GitHub one-line installer
```

`deploy/m-ui.service` and `deploy/m-ui.openrc` assume `/usr/local/m-ui` by default. The installer rewrites them when a custom installation directory is selected.

Sharing-link conversion reuses code derived from Mihomo's `common/convert`; source and GPL-3.0 attribution are documented in `internal/mihomoconvert/NOTICE.md`. Link parsing does not access remote subscription URLs.

## Acknowledgements

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo) — the proxy core and configuration model used by m-ui
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui) — interface density and workflow inspiration
* [Sub-Store](https://github.com/sub-store-org/Sub-Store) — reference behavior for several client subscription formats

## License

m-ui is licensed under the [GNU General Public License version 3](LICENSE). The Mihomo sharing-link conversion code retains its GPL-3.0 attribution in [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md).
