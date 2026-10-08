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

[Mihomo](https://github.com/MetaCubeX/mihomo) をコアとするサーバー管理パネルです。操作の流れは 3x-ui、画面デザインは Apple の Liquid Glass、設定モデルは Mihomo ネイティブの YAML に基づいています。パネルは 11 言語に対応しています。

> [!IMPORTANT]
> 本プロジェクトは、ご自身が所有する、または管理を許可されたサーバーとネットワークでのみ使用してください。現地の法律、上流プロバイダーの規約、利用するサービスのポリシーを守ってください。

詳しいドキュメントは [m-ui Wiki](https://github.com/RomanovCaesar/m-ui/wiki)（英語）をご覧ください。開発情報は [Telegram チャンネル](https://t.me/MihomoUI) でお知らせしています。

## 機能

**パネルと Mihomo**
* システム、トラフィック、接続、Mihomo の状態を表示するダッシュボード。Mihomo はパネルと一緒に自動起動します
* Mihomo ネイティブ YAML の生成と検証、生成された設定のプレビューとダウンロード
* MetaCubeX 公式リリースからの Mihomo バージョン切り替え、GeoIP / GeoSite / MetaDB の更新
* 完全なバックアップと復元：設定、インバウンド、Multi-control の ID（任意で復元）、パネル間キャッシュ、サブスクリプションテンプレート
* 英語、簡体字中国語、繁体字中国語、日本語、ロシア語、ペルシア語、ベトナム語、スペイン語、トルコ語、ウクライナ語、ブラジルポルトガル語の UI

**インバウンドとクライアント**
* `mixed`、`socks`、`http`、`shadowsocks`、`snell`、`vmess`、`vless`、`trojan`、`hysteria2`、`tuic`、`anytls`、`mieru`、`sudoku`、`shadowquic`、`trusttunnel`、`hysteria2-realm` のリスナー
* TLS、Reality、ShadowTLS、RestTLS、TLSMirror、JLS、Trojan SS、XHTTP、mKCP、Mekya、難読化の設定
* クライアントごとの認証情報、通信量の上限、有効期限、リセット周期、オンライン状態。インバウンド単位の上限も設定可能
* 共有リンク、QR コード、Mihomo リスナー YAML からのインバウンドのインポート、通信量の一括リセット

**サブスクリプション**
* 使用量、上限、有効期限、ノードを表示するブラウザ向けサブスクリプションページ
* **サブスクリプションテンプレート**：すべてのサブスクリプションが完全なクライアント設定になります。内蔵の中国 / ロシア / イラン版テンプレート、または独自の Mihomo テンプレートから生成します
* Mihomo/Clash、Stash、sing-box、Surge、Surfboard、Loon、Quantumult X、Egern 向けの完全な設定。V2Box、V2RayNG、Shadowrocket などの共有リンク型クライアントにはノード一覧
* ユーザーごとのサブスクリプショントークン、専用サブスクリプションポート（任意）

**Mihomo 設定**
* Basics：ルーティングモード、直接接続の IP バージョン、IPv6、TCP 同時接続、統計、ログ、基本ルーティング
* アウトバウンド（Snell、WireGuard、OpenVPN を含む）とポリシーグループ。フォーム、ネイティブ YAML、共有リンク変換に対応
* 上から順に評価されるルーティングルール
* Cloudflare WARP アウトバウンドと、国と ISP で選べる VPNGate の家庭用回線出口

**Multi-control**
* マスターのない P2P パネルネットワーク（署名付き ID）
* 信頼したパネル間での暗号化されたインバウンド同期
* パネル間サブスクリプションの集約

## Linux へのインストール

systemd または OpenRC を使う Linux で、`root` として実行します：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

インストーラーは未使用の 5 桁のパネルポートを選び、16 文字のパネルパス、14 文字のユーザー名、16 文字のパスワードを生成します。アクセス URL と認証情報は最後に一度だけ表示されます。明示的に指定することもできます：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

m-ui は `/usr/local/m-ui` にインストールされ、データは `/usr/local/m-ui/data` に保存されます。インストーラーは現在のアーキテクチャ用の Mihomo コアもダウンロードします。既存のコアは `--update-mihomo` を指定しない限り保持されます。

`m-ui update` は設定を保持したまま m-ui を更新します。更新前にデータディレクトリのスナップショットを `data/backups/` に作成し、最新の 3 つを保持します。

リリースには `386`、`amd64`、`arm64`、`armv5`、`armv6`、`armv7`、`s390x` 向けの `m-ui-linux-<architecture>.tar.gz` と `m-ui-windows-amd64.zip` が含まれます。

## Docker

Docker イメージは Linux amd64 向けで、m-ui と、バージョン固定かつ検証済みの Mihomo コアを含みます。Compose はホストネットワークを使うため、パネルで追加したインバウンドのポートは追加のポートマッピングなしで使えます。

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

`MUI_PASSWORD` と `MUI_PATH` が空の場合、初回起動時に自動生成され、URL と認証情報がログに一度だけ表示されます。各リリースは `ghcr.io/romanovcaesar/m-ui:<tag>` と `latest` も公開します。イメージを利用できる場合は、ビルドの代わりに `docker compose pull` を使えます。データは `m-ui-data` と `m-ui-core` のボリュームに保存されます。削除するつもりがない限り `docker compose down -v` は実行しないでください。

## TLS 証明書

初回の対話型インストールでは、Let's Encrypt のドメイン証明書、短期の Let's Encrypt IPv4 証明書、既存の証明書、または TLS を設定しない、から選べます。インストール後は次を実行します：

```bash
m-ui ssl
```

メニューでは、ドメイン証明書と IP 証明書の発行、ポート 80 が使えない場合の Cloudflare DNS API によるドメインとワイルドカード証明書の発行、更新、失効、一覧表示、既存の証明書の適用ができます。証明書ファイルにリーフ証明書しかない場合、m-ui は証明書チェーンを自動で補完します。Mihomo Party や FlClash などのクライアントがそのような証明書を拒否するためです。

## 管理コマンド

```text
m-ui                  interactive menu
m-ui start | stop | restart | status | logs
m-ui settings | configure | set-port
m-ui reset-credentials | reset-path
m-ui update | update-menu | ssl
m-ui enable | disable | clear-logs | uninstall
m-ui --lang <code>    menu language, e.g. m-ui --lang ja
```

管理メニューはパネルと同じ 11 言語に対応し、選んだ言語を記憶します。インストーラーは英語のままです。

## ドキュメント

Wiki は英語と簡体字中国語で提供しています：

* [Installation](https://github.com/RomanovCaesar/m-ui/wiki/Installation)、[Panel Configuration](https://github.com/RomanovCaesar/m-ui/wiki/Configuration)
* [Inbounds and Clients](https://github.com/RomanovCaesar/m-ui/wiki/Inbounds)、[Mihomo Settings](https://github.com/RomanovCaesar/m-ui/wiki/Mihomo-Settings)
* [Subscriptions](https://github.com/RomanovCaesar/m-ui/wiki/Subscriptions)、[Subscription Templates](https://github.com/RomanovCaesar/m-ui/wiki/Subscription-Templates)
* [Multi-control](https://github.com/RomanovCaesar/m-ui/wiki/Multi-control)、[Cloudflare WARP](https://github.com/RomanovCaesar/m-ui/wiki/Cloudflare-WARP)、[VPNGate](https://github.com/RomanovCaesar/m-ui/wiki/VPNGate)
* [Maintenance](https://github.com/RomanovCaesar/m-ui/wiki/Maintenance)、[API](https://github.com/RomanovCaesar/m-ui/wiki/API)、[Common Questions](https://github.com/RomanovCaesar/m-ui/wiki/Common-questions-and-problems)

## 開発

Windows でソースから実行します。パネルは `http://127.0.0.1:2053` で開き、アカウントは `admin` / `admin` です：

```powershell
.\scripts\run.ps1
```

Windows と Linux amd64 のバイナリ、またはすべてのリリースアーカイブをビルドします：

```powershell
.\scripts\build.ps1
.\scripts\build-release.ps1
```

`go test ./...` と `go vet ./...` で検証します。バージョンは `MUI_VERSION` または現在の Git タグから `-X main.version=...` で埋め込まれます。ソース構成とリリースの流れは [Development and Releases](https://github.com/RomanovCaesar/m-ui/wiki/Development)、UI の実装は [`LIQUID-GLASS.md`](LIQUID-GLASS.md) を参照してください。

## 謝辞

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo)：プロキシコアと設定モデル
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)：画面の情報密度と操作の流れ
* [Sub-Store](https://github.com/sub-store-org/Sub-Store)：クライアント向けサブスクリプション形式の参考実装
* [liquid-glass-webgl](https://github.com/martin65536/liquid-glass-webgl)：Liquid Glass コントロール（Apache-2.0、[`NOTICE`](NOTICE) を参照）

## ライセンス

m-ui は [GNU General Public License version 3](LICENSE) で提供されています。Mihomo の共有リンク変換コードの GPL-3.0 に関する表記は [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md) にあります。
