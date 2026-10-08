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

一个以 [Mihomo](https://github.com/MetaCubeX/mihomo) 为内核的服务端管理面板。操作流程参考 3x-ui，界面采用 Apple 液态玻璃（Liquid Glass）设计，配置模型使用 Mihomo 原生 YAML。面板支持 11 种语言。

> [!IMPORTANT]
> 本项目只应用于您拥有或获授权管理的服务器和网络。请遵守所在地法律、上游服务商条款以及您所访问服务的使用政策。

完整文档请参阅 [m-ui Wiki](https://github.com/RomanovCaesar/m-ui/wiki/Home.zh_CN)。也欢迎关注我们的 [Telegram 频道](https://t.me/MihomoUI) 获取开发动态。

## 功能

**面板与 Mihomo**
* 仪表盘显示系统、流量、连接和 Mihomo 状态；面板启动时自动启动 Mihomo
* 生成并校验 Mihomo 原生 YAML，可预览和下载原始配置
* 从 MetaCubeX 官方 Release 切换 Mihomo 版本，更新 GeoIP / GeoSite / MetaDB
* 完整备份与恢复：设置、Inbound、Multi-control 身份（可选恢复）、跨面板缓存和订阅模版
* 界面支持英语、简体中文、繁體中文、日语、俄语、波斯语、越南语、西班牙语、土耳其语、乌克兰语和巴西葡萄牙语

**Inbound 与 Client**
* 支持 `mixed`、`socks`、`http`、`shadowsocks`、`snell`、`vmess`、`vless`、`trojan`、`hysteria2`、`tuic`、`anytls`、`mieru`、`sudoku`、`shadowquic`、`trusttunnel` 和 `hysteria2-realm` 监听
* TLS、Reality、ShadowTLS、RestTLS、TLSMirror、JLS、Trojan SS、XHTTP、mKCP、Mekya 和混淆设置
* 每个 Client 的凭据、流量配额、到期时间、重置周期和在线状态；Inbound 也可设置总量和到期
* 分享链接、二维码、通过 Mihomo listener YAML 导入 Inbound、批量重置流量

**订阅**
* 浏览器订阅页，显示用量、配额、到期时间和节点
* **订阅模版**：每个订阅都是完整的客户端配置，可使用内置的中国 / 俄国 / 伊朗模版，也可导入自己的 Mihomo 模版
* 为 Mihomo/Clash、Stash、sing-box、Surge、Surfboard、Loon、Quantumult X、Egern 生成完整配置；为 V2Box、V2RayNG、Shadowrocket 等分享链接类客户端提供节点列表
* 每个用户独立的订阅 token，可选独立订阅端口

**Mihomo 设置**
* Basics：运行模式、直连 IP 版本、IPv6、TCP 并发、统计、日志和基础分流
* Outbounds（包括 Snell、WireGuard、OpenVPN）和策略组，支持表单、原生 YAML 和分享链接转换
* 自上而下匹配的路由规则
* Cloudflare WARP 出站，以及按国家和运营商选择的 VPNGate 家宽出口

**Multi-control**
* 无主节点的 P2P 面板网络，使用签名身份
* 在可信面板之间加密同步 Inbound
* 跨面板订阅聚合

## Linux 安装

在使用 systemd 或 OpenRC 的 Linux 上以 `root` 执行：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

安装器会选择一个未占用的五位数面板端口，并生成 16 位面板路径、14 位用户名和 16 位密码，安装结束时只打印一次访问地址和凭据。也可以显式指定：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

m-ui 安装在 `/usr/local/m-ui`，数据保存在 `/usr/local/m-ui/data`。安装器还会下载适配当前架构的 Mihomo 内核；已有内核默认保留，传入 `--update-mihomo` 才会替换。

`m-ui update` 升级 m-ui 并保留全部设置。更新前会把数据目录快照到 `data/backups/`，保留最近 3 份。

Release 提供 `386`、`amd64`、`arm64`、`armv5`、`armv6`、`armv7` 和 `s390x` 的 `m-ui-linux-<architecture>.tar.gz`，以及 `m-ui-windows-amd64.zip`。

## Docker

Docker 镜像面向 Linux amd64，包含 m-ui 和一个固定版本、经过校验的 Mihomo 内核。Compose 使用 host 网络，面板里新增的 Inbound 端口无需额外端口映射即可使用。

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

如果 `MUI_PASSWORD` 和 `MUI_PATH` 为空，首次启动会自动生成，并在日志里打印一次访问地址和凭据。每个版本也会发布 `ghcr.io/romanovcaesar/m-ui:<tag>` 和 `latest`；镜像可用时，可以用 `docker compose pull` 代替本地构建。数据保存在 `m-ui-data` 和 `m-ui-core` 两个卷中；除非确实要删除，否则不要执行 `docker compose down -v`。

## TLS 证书

首次交互安装时可以选择：Let's Encrypt 域名证书、Let's Encrypt 短期 IPv4 证书、已有证书和私钥，或跳过 TLS。安装后运行：

```bash
m-ui ssl
```

菜单可以申请域名和 IP 证书；80 端口不可用时可通过 Cloudflare DNS API 申请域名与通配符证书；还可以续期、吊销、查看证书，以及应用已有证书。证书文件只有叶子证书时，m-ui 会自动补全证书链，因为 Mihomo Party、FlClash 等客户端会拒绝这样的证书。

## 管理命令

```text
m-ui                  interactive menu
m-ui start | stop | restart | status | logs
m-ui settings | configure | set-port
m-ui reset-credentials | reset-path
m-ui update | update-menu | ssl
m-ui enable | disable | clear-logs | uninstall
m-ui --lang <code>    menu language, e.g. m-ui --lang zh-cn
```

管理菜单支持和面板相同的 11 种语言，并会记住你的选择。安装脚本保持英文。

## 文档

* [安装](https://github.com/RomanovCaesar/m-ui/wiki/Installation.zh_CN)、[面板设置](https://github.com/RomanovCaesar/m-ui/wiki/Configuration.zh_CN)
* [Inbounds 与 Clients](https://github.com/RomanovCaesar/m-ui/wiki/Inbounds.zh_CN)、[Mihomo 设置](https://github.com/RomanovCaesar/m-ui/wiki/Mihomo-Settings.zh_CN)
* [订阅](https://github.com/RomanovCaesar/m-ui/wiki/Subscriptions.zh_CN)、[订阅模版](https://github.com/RomanovCaesar/m-ui/wiki/Subscription-Templates.zh_CN)
* [Multi-control](https://github.com/RomanovCaesar/m-ui/wiki/Multi-control.zh_CN)、[Cloudflare WARP](https://github.com/RomanovCaesar/m-ui/wiki/Cloudflare-WARP.zh_CN)、[VPNGate](https://github.com/RomanovCaesar/m-ui/wiki/VPNGate.zh_CN)
* [维护与管理](https://github.com/RomanovCaesar/m-ui/wiki/Maintenance.zh_CN)、[API](https://github.com/RomanovCaesar/m-ui/wiki/API.zh_CN)、[常见问题](https://github.com/RomanovCaesar/m-ui/wiki/Common-questions-and-problems.zh_CN)

## 开发

在 Windows 上从源码运行，面板地址为 `http://127.0.0.1:2053`，账号 `admin` / `admin`：

```powershell
.\scripts\run.ps1
```

构建 Windows 和 Linux amd64 二进制，或全部 Release 压缩包：

```powershell
.\scripts\build.ps1
.\scripts\build-release.ps1
```

使用 `go test ./...` 和 `go vet ./...` 校验。版本号取自 `MUI_VERSION` 或当前 Git tag，通过 `-X main.version=...` 注入。源码结构和发布流程见[开发与发布](https://github.com/RomanovCaesar/m-ui/wiki/Development.zh_CN)，界面实现见 [`LIQUID-GLASS.md`](LIQUID-GLASS.md)。

## 致谢

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo)：代理内核与配置模型
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)：界面信息密度与操作流程
* [Sub-Store](https://github.com/sub-store-org/Sub-Store)：客户端订阅格式的参考实现
* [liquid-glass-webgl](https://github.com/martin65536/liquid-glass-webgl)：液态玻璃控件（Apache-2.0，见 [`NOTICE`](NOTICE)）

## 许可证

m-ui 使用 [GNU General Public License version 3](LICENSE) 授权。Mihomo 分享链接转换代码的 GPL-3.0 来源说明见 [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md)。
