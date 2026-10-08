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

[Mihomo](https://github.com/MetaCubeX/mihomo) çekirdeğini kullanan bir sunucu yönetim paneli. İş akışı 3x-ui'ye, arayüzü Apple'ın Liquid Glass tasarımına, yapılandırma modeli ise Mihomo'nun yerel YAML biçimine dayanır. Panel 11 dilde kullanılabilir.

> [!IMPORTANT]
> Bu projeyi yalnızca sahibi olduğunuz veya yönetme yetkiniz bulunan sunucu ve ağlarda kullanın. Yerel yasalara, sağlayıcı koşullarına ve eriştiğiniz hizmetlerin politikalarına uyun.

Tüm belgeler [m-ui Wiki](https://github.com/RomanovCaesar/m-ui/wiki) sayfasındadır (İngilizce). Geliştirme haberleri için [Telegram kanalımızı](https://t.me/MihomoUI) takip edin.

## Özellikler

**Panel ve Mihomo**
* Sistem, trafik, bağlantı ve Mihomo durumunu gösteren gösterge paneli; Mihomo panelle birlikte otomatik başlar
* Mihomo'nun yerel YAML yapılandırmasını oluşturma ve doğrulama, yapılandırmayı önizleme ve indirme
* MetaCubeX'in resmi sürümlerinden Mihomo sürümü değiştirme, GeoIP / GeoSite / MetaDB güncelleme
* Tam yedekleme ve geri yükleme: ayarlar, girişler, Multi-control kimliği (isteğe bağlı), paneller arası önbellek ve abonelik şablonları
* İngilizce, Basitleştirilmiş Çince, Geleneksel Çince, Japonca, Rusça, Farsça, Vietnamca, İspanyolca, Türkçe, Ukraynaca ve Brezilya Portekizcesi arayüz

**Girişler ve istemciler**
* `mixed`, `socks`, `http`, `shadowsocks`, `snell`, `vmess`, `vless`, `trojan`, `hysteria2`, `tuic`, `anytls`, `mieru`, `sudoku`, `shadowquic`, `trusttunnel` ve `hysteria2-realm` dinleyicileri
* TLS, Reality, ShadowTLS, RestTLS, TLSMirror, JLS, Trojan SS, XHTTP, mKCP, Mekya ve gizleme ayarları
* İstemci başına kimlik bilgileri, trafik kotası, son kullanma tarihi, periyodik sıfırlama ve çevrimiçi durum; giriş düzeyinde sınırlar da var
* Paylaşım bağlantıları, QR kodları, Mihomo dinleyici YAML'ından giriş içe aktarma ve toplu trafik sıfırlama

**Abonelikler**
* Kullanım, kota, son kullanma tarihi ve düğümleri gösteren tarayıcı abonelik sayfası
* **Abonelik şablonları:** her abonelik, yerleşik şablondan (Çin / Rusya / İran) veya kendi Mihomo şablonunuzdan oluşturulan eksiksiz bir istemci profilidir
* Mihomo/Clash, Stash, sing-box, Surge, Surfboard, Loon, Quantumult X ve Egern için tam profiller; V2Box, V2RayNG, Shadowrocket ve diğer bağlantı tabanlı istemciler için düğüm listeleri
* Kullanıcı başına abonelik belirteci ve isteğe bağlı ayrı abonelik portu

**Mihomo Ayarları**
* Basics: yönlendirme modu, doğrudan bağlantılar için IP sürümü, IPv6, eşzamanlı TCP, istatistikler, günlükler ve temel yönlendirme
* Çıkışlar (Snell, WireGuard ve OpenVPN dahil) ve politika grupları; form, yerel YAML ve bağlantı dönüştürme ile
* Yukarıdan aşağıya değerlendirilen yönlendirme kuralları
* Cloudflare WARP çıkışları ve ülke ile operatöre göre VPNGate ev interneti çıkışları

**Multi-control**
* Ana düğümü olmayan, imzalı kimlikler kullanan P2P panel ağı
* Güvenilen paneller arasında şifreli giriş eşitleme
* Paneller arası abonelik birleştirme

## Linux kurulumu

systemd veya OpenRC kullanan bir Linux sisteminde `root` olarak çalıştırın:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

Yükleyici boş bir beş haneli panel portu seçer; 16 karakterlik bir panel yolu, 14 karakterlik bir kullanıcı adı ve 16 karakterlik bir parola oluşturur. Erişim adresi ve kimlik bilgileri sonunda yalnızca bir kez gösterilir. Bunları kendiniz de belirleyebilirsiniz:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

m-ui `/usr/local/m-ui` dizinine kurulur ve verilerini `/usr/local/m-ui/data` içinde tutar. Yükleyici geçerli mimari için bir Mihomo çekirdeği de indirir; mevcut çekirdek, `--update-mihomo` kullanılmadıkça korunur.

`m-ui update`, tüm ayarları koruyarak m-ui'yi günceller. Güncellemeden önce veri dizininin anlık görüntüsünü `data/backups/` içine alır ve en yeni 3 tanesini saklar.

Her sürümde `386`, `amd64`, `arm64`, `armv5`, `armv6`, `armv7` ve `s390x` için `m-ui-linux-<architecture>.tar.gz` ile `m-ui-windows-amd64.zip` bulunur.

## Docker

Docker imajı Linux amd64 içindir ve m-ui ile sabit sürümlü, doğrulanmış bir Mihomo çekirdeği içerir. Compose ana makine ağını kullanır; panelde eklediğiniz girişlerin portları ek port eşlemesi gerekmeden çalışır.

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

`MUI_PASSWORD` ve `MUI_PATH` boşsa ilk başlatmada oluşturulur; adres ve kimlik bilgileri günlüğe bir kez yazılır. Her sürüm ayrıca `ghcr.io/romanovcaesar/m-ui:<tag>` ve `latest` yayınlar; imaj sizin için erişilebilirse derlemek yerine `docker compose pull` kullanabilirsiniz. Veriler `m-ui-data` ve `m-ui-core` birimlerinde tutulur; silmek istemiyorsanız `docker compose down -v` çalıştırmayın.

## TLS sertifikaları

İlk etkileşimli kurulum; alan adı için Let's Encrypt sertifikası, IPv4 için kısa ömürlü Let's Encrypt sertifikası, mevcut bir sertifika ya da TLS'yi atlama seçenekleri sunar. Kurulumdan sonra şunu çalıştırın:

```bash
m-ui ssl
```

Menü; alan adı ve IP sertifikaları almayı, 80 numaralı port kullanılamadığında Cloudflare DNS API ile alan adı ve joker sertifika almayı, yenilemeyi, iptal etmeyi, listelemeyi ve mevcut bir sertifikayı uygulamayı sağlar. Sertifika dosyası yalnızca son sertifikayı içeriyorsa m-ui zinciri otomatik tamamlar, çünkü Mihomo Party ve FlClash gibi istemciler böyle sertifikaları reddeder.

## Yönetim komutları

```text
m-ui                  interactive menu
m-ui start | stop | restart | status | logs
m-ui settings | configure | set-port
m-ui reset-credentials | reset-path
m-ui update | update-menu | ssl
m-ui enable | disable | clear-logs | uninstall
m-ui --lang <code>    menu language, e.g. m-ui --lang tr
```

Menü, panelle aynı 11 dili destekler ve seçiminizi hatırlar. Yükleyici İngilizce kalır.

## Belgeler

Wiki İngilizce ve Basitleştirilmiş Çince olarak sunulur:

* [Installation](https://github.com/RomanovCaesar/m-ui/wiki/Installation), [Panel Configuration](https://github.com/RomanovCaesar/m-ui/wiki/Configuration)
* [Inbounds and Clients](https://github.com/RomanovCaesar/m-ui/wiki/Inbounds), [Mihomo Settings](https://github.com/RomanovCaesar/m-ui/wiki/Mihomo-Settings)
* [Subscriptions](https://github.com/RomanovCaesar/m-ui/wiki/Subscriptions), [Subscription Templates](https://github.com/RomanovCaesar/m-ui/wiki/Subscription-Templates)
* [Multi-control](https://github.com/RomanovCaesar/m-ui/wiki/Multi-control), [Cloudflare WARP](https://github.com/RomanovCaesar/m-ui/wiki/Cloudflare-WARP), [VPNGate](https://github.com/RomanovCaesar/m-ui/wiki/VPNGate)
* [Maintenance](https://github.com/RomanovCaesar/m-ui/wiki/Maintenance), [API](https://github.com/RomanovCaesar/m-ui/wiki/API), [Common Questions](https://github.com/RomanovCaesar/m-ui/wiki/Common-questions-and-problems)

## Geliştirme

Windows'ta kaynak koddan çalıştırma; panel `http://127.0.0.1:2053` adresinde `admin` / `admin` hesabıyla açılır:

```powershell
.\scripts\run.ps1
```

Windows ve Linux amd64 ikili dosyalarını ya da tüm sürüm arşivlerini derleyin:

```powershell
.\scripts\build.ps1
.\scripts\build-release.ps1
```

`go test ./...` ve `go vet ./...` ile doğrulayın. Sürüm, `MUI_VERSION` değişkeninden veya geçerli Git etiketinden `-X main.version=...` ile eklenir. Kaynak yapısı ve yayın süreci için [Development and Releases](https://github.com/RomanovCaesar/m-ui/wiki/Development), arayüz için [`LIQUID-GLASS.md`](LIQUID-GLASS.md) dosyasına bakın.

## Teşekkürler

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo): proxy çekirdeği ve yapılandırma modeli
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui): arayüz yoğunluğu ve iş akışı
* [Sub-Store](https://github.com/sub-store-org/Sub-Store): istemci abonelik biçimleri için başvuru davranışı
* [liquid-glass-webgl](https://github.com/martin65536/liquid-glass-webgl): Liquid Glass denetimleri (Apache-2.0, bkz. [`NOTICE`](NOTICE))

## Lisans

m-ui, [GNU General Public License version 3](LICENSE) lisansıyla dağıtılır. Mihomo bağlantı dönüştürme kodu GPL-3.0 atfını [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md) içinde korur.
