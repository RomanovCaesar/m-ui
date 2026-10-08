[English](/README.md) | [简体中文](/README.zh_CN.md) | [繁體中文](/README.zh_TW.md) | [日本語](/README.ja.md) | [Русский](/README.ru.md) | [فارسی](/README.fa.md) | [Tiếng Việt](/README.vi.md) | [Español](/README.es.md) | [Türkçe](/README.tr.md) | [Українська](/README.uk.md) | [Português (Brasil)](/README.pt_BR.md)

<p align="center">
  <img src="./media/m-ui_logo.png" alt="m-ui logo" width="420">
</p>

# m-ui

[![Release](https://img.shields.io/github/v/release/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/releases)
[![Build](https://img.shields.io/github/actions/workflow/status/RomanovCaesar/m-ui/release.yml.svg)](https://github.com/RomanovCaesar/m-ui/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/blob/main/go.mod)
[![Downloads](https://img.shields.io/github/downloads/RomanovCaesar/m-ui/total.svg)](https://github.com/RomanovCaesar/m-ui/releases/latest)
[![License](https://img.shields.io/github/license/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/blob/main/LICENSE)

A server management panel powered by [Mihomo](https://github.com/MetaCubeX/mihomo). Its workflow follows 3x-ui, its interface follows Apple's Liquid Glass design, and its configuration model is Mihomo's native YAML. The panel is available in 11 languages.

> [!IMPORTANT]
> Use this project only on servers and networks that you own or are authorized to administer. Follow local law, upstream provider terms, and the policies of the services you access.

For complete documentation, visit the [m-ui Wiki](https://github.com/RomanovCaesar/m-ui/wiki). Subscribe to our [Telegram channel](https://t.me/MihomoUI) for development news.

## Features

**Panel and Mihomo**
* Dashboard with system, traffic, connection, and Mihomo status; Mihomo starts automatically with the panel
* Native Mihomo YAML generation and validation, raw configuration preview and download
* Mihomo version switching from official MetaCubeX releases, and GeoIP / GeoSite / MetaDB updates
* Full backup and restore: settings, inbounds, Multi-control identity (opt-in), cross-panel cache, and subscription templates
* Interface in English, Simplified Chinese, Traditional Chinese, Japanese, Russian, Persian, Vietnamese, Spanish, Turkish, Ukrainian, and Brazilian Portuguese

**Inbounds and clients**
* `mixed`, `socks`, `http`, `shadowsocks`, `snell`, `vmess`, `vless`, `trojan`, `hysteria2`, `tuic`, `anytls`, `mieru`, `sudoku`, `shadowquic`, `trusttunnel`, and `hysteria2-realm` listeners
* TLS, Reality, ShadowTLS, RestTLS, TLSMirror, JLS, Trojan SS, XHTTP, mKCP, Mekya, and obfuscation settings
* Per-client credentials, traffic quotas, expiry, reset schedules, and online state; inbound-level limits too
* Share links, QR codes, inbound import from Mihomo listener YAML, and bulk traffic reset

**Subscriptions**
* Browser subscription page with usage, quota, expiry, and nodes
* **Subscription templates:** every subscription is a complete client profile built from the built-in China / Russia / Iran template or your own Mihomo template
* Full profiles for Mihomo/Clash, Stash, sing-box, Surge, Surfboard, Loon, Quantumult X, and Egern; node lists for V2Box, V2RayNG, Shadowrocket, and other share-link clients
* Per-user subscription tokens and an optional dedicated subscription port

**Mihomo Settings**
* Basics: routing mode, direct IP version, IPv6, TCP concurrency, statistics, logs, and basic routing
* Outbounds (including Snell, WireGuard, and OpenVPN) and policy groups, with a form editor, native YAML, and share-link conversion
* Ordered routing rules
* Cloudflare WARP outbounds and VPNGate residential exits by country and ISP

**Multi-control**
* Peer-to-peer panel network with signed identities and no master node
* Encrypted inbound synchronization between trusted panels
* Cross-panel subscription aggregation

## Linux installation

Run as `root` on a Linux system using systemd or OpenRC:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

The installer picks an unused five-digit panel port and generates a 16-character panel path, a 14-character username, and a 16-character password. It prints the access URL and credentials once at the end. You can also set them explicitly:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

m-ui is installed under `/usr/local/m-ui`, with its data in `/usr/local/m-ui/data`. The installer also downloads a Mihomo core for the current architecture; an existing core is kept unless you pass `--update-mihomo`.

`m-ui update` upgrades m-ui and keeps all settings. Before updating, it snapshots the data directory into `data/backups/` and keeps the newest 3 snapshots.

Releases contain `m-ui-linux-<architecture>.tar.gz` for `386`, `amd64`, `arm64`, `armv5`, `armv6`, `armv7`, and `s390x`, plus `m-ui-windows-amd64.zip`.

## Docker

The Docker image targets Linux amd64 and contains m-ui plus a pinned, checksum-verified Mihomo core. The Compose file uses host networking, so inbound ports added in the panel work without extra port mappings.

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

If `MUI_PASSWORD` and `MUI_PATH` are empty, the first start generates them and prints the URL and credentials once in the log. Releases also publish `ghcr.io/romanovcaesar/m-ui:<tag>` and `latest`; use `docker compose pull` instead of building when the image is available to you. Data lives in the `m-ui-data` and `m-ui-core` volumes; do not run `docker compose down -v` unless you want to delete it.

## TLS certificates

The first interactive installation offers a Let's Encrypt domain certificate, a short-lived Let's Encrypt IPv4 certificate, an existing certificate pair, or skipping TLS. After installation, run:

```bash
m-ui ssl
```

The menu can issue domain and IP certificates, use the Cloudflare DNS API for domain and wildcard certificates when port 80 is unavailable, renew, revoke, list, and apply an existing certificate pair. m-ui completes certificate files that contain only the leaf certificate, because clients such as Mihomo Party and FlClash reject them.

## Management commands

```text
m-ui                  interactive menu
m-ui start | stop | restart | status | logs
m-ui settings | configure | set-port
m-ui reset-credentials | reset-path
m-ui update | update-menu | ssl
m-ui enable | disable | clear-logs | uninstall
m-ui --lang <code>    menu language, e.g. m-ui --lang zh-cn
```

The menu speaks the same 11 languages as the panel and remembers your choice. The installer stays in English.

## Documentation

* [Installation](https://github.com/RomanovCaesar/m-ui/wiki/Installation) and [Panel Configuration](https://github.com/RomanovCaesar/m-ui/wiki/Configuration)
* [Inbounds and Clients](https://github.com/RomanovCaesar/m-ui/wiki/Inbounds) and [Mihomo Settings](https://github.com/RomanovCaesar/m-ui/wiki/Mihomo-Settings)
* [Subscriptions](https://github.com/RomanovCaesar/m-ui/wiki/Subscriptions) and [Subscription Templates](https://github.com/RomanovCaesar/m-ui/wiki/Subscription-Templates)
* [Multi-control](https://github.com/RomanovCaesar/m-ui/wiki/Multi-control), [Cloudflare WARP](https://github.com/RomanovCaesar/m-ui/wiki/Cloudflare-WARP), and [VPNGate](https://github.com/RomanovCaesar/m-ui/wiki/VPNGate)
* [Maintenance](https://github.com/RomanovCaesar/m-ui/wiki/Maintenance), [API](https://github.com/RomanovCaesar/m-ui/wiki/API), and [Common Questions](https://github.com/RomanovCaesar/m-ui/wiki/Common-questions-and-problems)

## Development

Run from source on Windows; the panel opens at `http://127.0.0.1:2053` with the account `admin` / `admin`:

```powershell
.\scripts\run.ps1
```

Build Windows and Linux amd64 binaries, or all release archives:

```powershell
.\scripts\build.ps1
.\scripts\build-release.ps1
```

Validate with `go test ./...` and `go vet ./...`. The version is injected with `-X main.version=...` from `MUI_VERSION` or the current Git tag. See [Development and Releases](https://github.com/RomanovCaesar/m-ui/wiki/Development) for the source layout and release flow, and [`LIQUID-GLASS.md`](LIQUID-GLASS.md) for the interface.

## Acknowledgements

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo): the proxy core and configuration model
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui): interface density and workflow
* [Sub-Store](https://github.com/sub-store-org/Sub-Store): reference behavior for client subscription formats
* [liquid-glass-webgl](https://github.com/martin65536/liquid-glass-webgl): the Liquid Glass controls (Apache-2.0, see [`NOTICE`](NOTICE))

## License

m-ui is licensed under the [GNU General Public License version 3](LICENSE). The Mihomo sharing-link conversion code keeps its GPL-3.0 attribution in [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md).
