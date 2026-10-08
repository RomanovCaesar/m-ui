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

一個以 [Mihomo](https://github.com/MetaCubeX/mihomo) 為核心的伺服器管理面板。操作流程參考 3x-ui，介面採用 Apple 液態玻璃（Liquid Glass）設計，設定模型使用 Mihomo 原生 YAML。面板支援 11 種語言。

> [!IMPORTANT]
> 本專案只應用於您擁有或獲授權管理的伺服器和網路。請遵守所在地法律、上游服務商條款，以及您所存取服務的使用政策。

完整文件請參閱 [m-ui Wiki](https://github.com/RomanovCaesar/m-ui/wiki/Home.zh_CN)（簡體中文）。也歡迎關注我們的 [Telegram 頻道](https://t.me/MihomoUI) 取得開發動態。

## 功能

**面板與 Mihomo**
* 儀表板顯示系統、流量、連線和 Mihomo 狀態；面板啟動時自動啟動 Mihomo
* 產生並驗證 Mihomo 原生 YAML，可預覽和下載原始設定
* 從 MetaCubeX 官方 Release 切換 Mihomo 版本，更新 GeoIP / GeoSite / MetaDB
* 完整備份與還原：設定、Inbound、Multi-control 身分（可選還原）、跨面板快取和訂閱範本
* 介面支援英文、簡體中文、繁體中文、日文、俄文、波斯文、越南文、西班牙文、土耳其文、烏克蘭文和巴西葡萄牙文

**Inbound 與 Client**
* 支援 `mixed`、`socks`、`http`、`shadowsocks`、`snell`、`vmess`、`vless`、`trojan`、`hysteria2`、`tuic`、`anytls`、`mieru`、`sudoku`、`shadowquic`、`trusttunnel` 和 `hysteria2-realm` 監聽
* TLS、Reality、ShadowTLS、RestTLS、TLSMirror、JLS、Trojan SS、XHTTP、mKCP、Mekya 和混淆設定
* 每個 Client 的憑證、流量配額、到期時間、重設週期和線上狀態；Inbound 也可設定總量和到期
* 分享連結、QR Code、透過 Mihomo listener YAML 匯入 Inbound、批次重設流量

**訂閱**
* 瀏覽器訂閱頁，顯示用量、配額、到期時間和節點
* **訂閱範本**：每個訂閱都是完整的用戶端設定，可使用內建的中國 / 俄羅斯 / 伊朗範本，也可匯入自己的 Mihomo 範本
* 為 Mihomo/Clash、Stash、sing-box、Surge、Surfboard、Loon、Quantumult X、Egern 產生完整設定；為 V2Box、V2RayNG、Shadowrocket 等分享連結類用戶端提供節點清單
* 每個使用者獨立的訂閱 token，可選獨立訂閱連接埠

**Mihomo 設定**
* Basics：執行模式、直連 IP 版本、IPv6、TCP 並行、統計、日誌和基礎分流
* Outbounds（包括 Snell、WireGuard、OpenVPN）和策略組，支援表單、原生 YAML 和分享連結轉換
* 由上而下比對的路由規則
* Cloudflare WARP 出站，以及依國家和電信業者選擇的 VPNGate 家用寬頻出口

**Multi-control**
* 沒有主節點的 P2P 面板網路，使用簽章身分
* 在受信任的面板之間加密同步 Inbound
* 跨面板訂閱聚合

## Linux 安裝

在使用 systemd 或 OpenRC 的 Linux 上以 `root` 執行：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

安裝程式會選擇一個未被占用的五位數面板連接埠，並產生 16 位面板路徑、14 位使用者名稱和 16 位密碼，安裝結束時只顯示一次存取網址和憑證。也可以明確指定：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

m-ui 安裝在 `/usr/local/m-ui`，資料儲存在 `/usr/local/m-ui/data`。安裝程式也會下載適用於目前架構的 Mihomo 核心；已有核心預設保留，加上 `--update-mihomo` 才會替換。

`m-ui update` 會升級 m-ui 並保留全部設定。更新前會把資料目錄快照到 `data/backups/`，保留最近 3 份。

Release 提供 `386`、`amd64`、`arm64`、`armv5`、`armv6`、`armv7` 和 `s390x` 的 `m-ui-linux-<architecture>.tar.gz`，以及 `m-ui-windows-amd64.zip`。

## Docker

Docker 映像檔適用於 Linux amd64，內含 m-ui 和一個固定版本、經過校驗的 Mihomo 核心。Compose 使用 host 網路，面板裡新增的 Inbound 連接埠不需要額外的連接埠對應即可使用。

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

如果 `MUI_PASSWORD` 和 `MUI_PATH` 為空，首次啟動會自動產生，並在日誌裡顯示一次存取網址和憑證。每個版本也會發布 `ghcr.io/romanovcaesar/m-ui:<tag>` 和 `latest`；映像檔可用時，可以用 `docker compose pull` 取代本機建置。資料儲存在 `m-ui-data` 和 `m-ui-core` 兩個磁碟區；除非確定要刪除，否則不要執行 `docker compose down -v`。

## TLS 憑證

首次互動安裝時可以選擇：Let's Encrypt 網域憑證、Let's Encrypt 短期 IPv4 憑證、既有憑證和私鑰，或略過 TLS。安裝後執行：

```bash
m-ui ssl
```

選單可以申請網域和 IP 憑證；80 連接埠無法使用時，可透過 Cloudflare DNS API 申請網域與萬用字元憑證；也可以續期、撤銷、檢視憑證，以及套用既有憑證。憑證檔案只有葉憑證時，m-ui 會自動補齊憑證鏈，因為 Mihomo Party、FlClash 等用戶端會拒絕這種憑證。

## 管理指令

```text
m-ui                  interactive menu
m-ui start | stop | restart | status | logs
m-ui settings | configure | set-port
m-ui reset-credentials | reset-path
m-ui update | update-menu | ssl
m-ui enable | disable | clear-logs | uninstall
m-ui --lang <code>    menu language, e.g. m-ui --lang zh-tw
```

管理選單支援和面板相同的 11 種語言，並會記住你的選擇。安裝程式維持英文。

## 文件

以下 Wiki 頁面為簡體中文：

* [安裝](https://github.com/RomanovCaesar/m-ui/wiki/Installation.zh_CN)、[面板設定](https://github.com/RomanovCaesar/m-ui/wiki/Configuration.zh_CN)
* [Inbounds 與 Clients](https://github.com/RomanovCaesar/m-ui/wiki/Inbounds.zh_CN)、[Mihomo 設定](https://github.com/RomanovCaesar/m-ui/wiki/Mihomo-Settings.zh_CN)
* [訂閱](https://github.com/RomanovCaesar/m-ui/wiki/Subscriptions.zh_CN)、[訂閱範本](https://github.com/RomanovCaesar/m-ui/wiki/Subscription-Templates.zh_CN)
* [Multi-control](https://github.com/RomanovCaesar/m-ui/wiki/Multi-control.zh_CN)、[Cloudflare WARP](https://github.com/RomanovCaesar/m-ui/wiki/Cloudflare-WARP.zh_CN)、[VPNGate](https://github.com/RomanovCaesar/m-ui/wiki/VPNGate.zh_CN)
* [維護與管理](https://github.com/RomanovCaesar/m-ui/wiki/Maintenance.zh_CN)、[API](https://github.com/RomanovCaesar/m-ui/wiki/API.zh_CN)、[常見問題](https://github.com/RomanovCaesar/m-ui/wiki/Common-questions-and-problems.zh_CN)

## 開發

在 Windows 上從原始碼執行，面板網址為 `http://127.0.0.1:2053`，帳號 `admin` / `admin`：

```powershell
.\scripts\run.ps1
```

建置 Windows 和 Linux amd64 執行檔，或全部 Release 壓縮檔：

```powershell
.\scripts\build.ps1
.\scripts\build-release.ps1
```

使用 `go test ./...` 和 `go vet ./...` 驗證。版本號取自 `MUI_VERSION` 或目前的 Git tag，透過 `-X main.version=...` 注入。原始碼結構和發布流程見[開發與發布](https://github.com/RomanovCaesar/m-ui/wiki/Development.zh_CN)，介面實作見 [`LIQUID-GLASS.md`](LIQUID-GLASS.md)。

## 致謝

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo)：代理核心與設定模型
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)：介面資訊密度與操作流程
* [Sub-Store](https://github.com/sub-store-org/Sub-Store)：用戶端訂閱格式的參考實作
* [liquid-glass-webgl](https://github.com/martin65536/liquid-glass-webgl)：液態玻璃控制項（Apache-2.0，見 [`NOTICE`](NOTICE)）

## 授權

m-ui 採用 [GNU General Public License version 3](LICENSE) 授權。Mihomo 分享連結轉換程式碼的 GPL-3.0 來源說明見 [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md)。
