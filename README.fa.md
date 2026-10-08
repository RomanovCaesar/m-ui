[English](/README.md) | [简体中文](/README.zh_CN.md) | [繁體中文](/README.zh_TW.md) | [日本語](/README.ja.md) | [Русский](/README.ru.md) | [فارسی](/README.fa.md) | [Tiếng Việt](/README.vi.md) | [Español](/README.es.md) | [Türkçe](/README.tr.md) | [Українська](/README.uk.md) | [Português (Brasil)](/README.pt_BR.md)

<div dir="rtl">

<p align="center">
  <img src="./media/m-ui_logo.png" alt="m-ui logo" width="420">
</p>

# m-ui

[![Release](https://img.shields.io/github/v/release/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/releases)
[![Build](https://img.shields.io/github/actions/workflow/status/RomanovCaesar/m-ui/release.yml.svg)](https://github.com/RomanovCaesar/m-ui/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/blob/main/go.mod)
[![Downloads](https://img.shields.io/github/downloads/RomanovCaesar/m-ui/total.svg)](https://github.com/RomanovCaesar/m-ui/releases/latest)
[![License](https://img.shields.io/github/license/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/blob/main/LICENSE)

یک پنل مدیریت سرور بر پایهٔ هستهٔ [Mihomo](https://github.com/MetaCubeX/mihomo). روند کار آن از 3x-ui، رابط کاربری آن از طراحی Liquid Glass اپل و مدل پیکربندی آن از YAML بومی Mihomo پیروی می‌کند. پنل به ۱۱ زبان در دسترس است.

> [!IMPORTANT]
> این پروژه را فقط روی سرورها و شبکه‌هایی به کار ببرید که مالک آن‌ها هستید یا اجازهٔ مدیریتشان را دارید. قوانین محلی، شرایط ارائه‌دهندگان و سیاست‌های سرویس‌هایی را که به آن‌ها دسترسی دارید رعایت کنید.

مستندات کامل در [ویکی m-ui](https://github.com/RomanovCaesar/m-ui/wiki) (به انگلیسی) است. برای اخبار توسعه، [کانال تلگرام](https://t.me/MihomoUI) ما را دنبال کنید.

## امکانات

**پنل و Mihomo**
* داشبورد وضعیت سیستم، ترافیک، اتصال‌ها و Mihomo؛ Mihomo همراه با پنل به‌طور خودکار اجرا می‌شود
* تولید و اعتبارسنجی پیکربندی YAML بومی Mihomo، پیش‌نمایش و دانلود پیکربندی
* تغییر نسخهٔ Mihomo از انتشارهای رسمی MetaCubeX و به‌روزرسانی GeoIP / GeoSite / MetaDB
* پشتیبان‌گیری و بازیابی کامل: تنظیمات، ورودی‌ها، هویت Multi-control (اختیاری)، حافظهٔ میان‌پنلی و الگوهای اشتراک
* رابط کاربری به زبان‌های انگلیسی، چینی ساده‌شده، چینی سنتی، ژاپنی، روسی، فارسی، ویتنامی، اسپانیایی، ترکی، اوکراینی و پرتغالی برزیل

**ورودی‌ها و کلاینت‌ها**
* شنونده‌های `mixed`، `socks`، `http`، `shadowsocks`، `snell`، `vmess`، `vless`، `trojan`، `hysteria2`، `tuic`، `anytls`، `mieru`، `sudoku`، `shadowquic`، `trusttunnel` و `hysteria2-realm`
* تنظیمات TLS، Reality، ShadowTLS، RestTLS، TLSMirror، JLS، Trojan SS، XHTTP، mKCP، Mekya و مبهم‌سازی
* اطلاعات احراز هویت، سهمیهٔ ترافیک، تاریخ انقضا، بازنشانی دوره‌ای و وضعیت آنلاین برای هر کلاینت؛ محدودیت در سطح ورودی هم وجود دارد
* لینک‌های اشتراک‌گذاری، کد QR، درون‌ریزی ورودی از YAML شنوندهٔ Mihomo و بازنشانی گروهی ترافیک

**اشتراک‌ها**
* صفحهٔ اشتراک در مرورگر با مصرف، سهمیه، تاریخ انقضا و گره‌ها
* **الگوهای اشتراک:** هر اشتراک یک پیکربندی کامل کلاینت است که از الگوی داخلی (چین / روسیه / ایران) یا الگوی Mihomo خودتان ساخته می‌شود
* پیکربندی کامل برای Mihomo/Clash، Stash، sing-box، Surge، Surfboard، Loon، Quantumult X و Egern؛ فهرست گره‌ها برای V2Box، V2RayNG، Shadowrocket و دیگر کلاینت‌های مبتنی بر لینک
* توکن اشتراک جداگانه برای هر کاربر و پورت اشتراک اختصاصی (اختیاری)

**تنظیمات Mihomo**
* Basics: حالت مسیریابی، نسخهٔ IP برای اتصال مستقیم، IPv6، TCP همزمان، آمار، لاگ‌ها و مسیریابی پایه
* خروجی‌ها (از جمله Snell، WireGuard و OpenVPN) و گروه‌های سیاست، با فرم، YAML بومی و تبدیل لینک
* قوانین مسیریابی که از بالا به پایین بررسی می‌شوند
* خروجی‌های Cloudflare WARP و خروجی IP خانگی VPNGate بر اساس کشور و اپراتور

**Multi-control**
* شبکهٔ P2P پنل‌ها بدون گرهٔ اصلی، با هویت‌های امضاشده
* همگام‌سازی رمزگذاری‌شدهٔ ورودی‌ها میان پنل‌های مورد اعتماد
* تجمیع اشتراک‌ها از چند پنل

## نصب روی لینوکس

روی لینوکسی که از systemd یا OpenRC استفاده می‌کند، با کاربر `root` اجرا کنید:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

نصب‌کننده یک پورت پنج‌رقمی آزاد انتخاب می‌کند و یک مسیر پنل ۱۶ نویسه‌ای، یک نام کاربری ۱۴ نویسه‌ای و یک رمز عبور ۱۶ نویسه‌ای می‌سازد. نشانی دسترسی و اطلاعات ورود فقط یک بار در پایان نمایش داده می‌شود. می‌توانید آن‌ها را خودتان هم تعیین کنید:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

m-ui در `/usr/local/m-ui` نصب می‌شود و داده‌هایش را در `/usr/local/m-ui/data` نگه می‌دارد. نصب‌کننده یک هستهٔ Mihomo متناسب با معماری فعلی هم دانلود می‌کند؛ هستهٔ موجود حفظ می‌شود، مگر اینکه `--update-mihomo` را بدهید.

`m-ui update` با حفظ همهٔ تنظیمات m-ui را به‌روز می‌کند. پیش از به‌روزرسانی، از پوشهٔ داده در `data/backups/` تصویر لحظه‌ای می‌گیرد و ۳ نسخهٔ آخر را نگه می‌دارد.

هر انتشار شامل `m-ui-linux-<architecture>.tar.gz` برای `386`، `amd64`، `arm64`، `armv5`، `armv6`، `armv7` و `s390x` و همچنین `m-ui-windows-amd64.zip` است.

## داکر

ایمیج داکر برای Linux amd64 است و m-ui را همراه با یک هستهٔ Mihomo با نسخهٔ ثابت و بررسی‌شده در خود دارد. Compose از شبکهٔ میزبان استفاده می‌کند، بنابراین پورت ورودی‌هایی که در پنل اضافه می‌کنید بدون نگاشت پورت جداگانه کار می‌کنند.

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

اگر `MUI_PASSWORD` و `MUI_PATH` خالی باشند، در نخستین اجرا ساخته می‌شوند و نشانی و اطلاعات ورود یک بار در لاگ نمایش داده می‌شود. هر انتشار همچنین `ghcr.io/romanovcaesar/m-ui:<tag>` و `latest` را منتشر می‌کند؛ اگر ایمیج برای شما در دسترس است، به جای ساخت محلی از `docker compose pull` استفاده کنید. داده‌ها در حجم‌های `m-ui-data` و `m-ui-core` نگه داشته می‌شوند؛ اگر نمی‌خواهید آن‌ها را پاک کنید، `docker compose down -v` را اجرا نکنید.

## گواهی‌های TLS

نخستین نصب تعاملی این گزینه‌ها را ارائه می‌دهد: گواهی Let's Encrypt برای دامنه، گواهی کوتاه‌مدت Let's Encrypt برای IPv4، گواهی موجود، یا رد شدن از TLS. پس از نصب اجرا کنید:

```bash
m-ui ssl
```

این منو می‌تواند گواهی دامنه و IP صادر کند، وقتی پورت 80 در دسترس نیست با API DNS کلادفلر گواهی دامنه و wildcard بگیرد، و گواهی‌ها را تمدید، ابطال، فهرست و روی پنل اعمال کند. اگر فایل گواهی فقط گواهی نهایی را داشته باشد، m-ui زنجیرهٔ گواهی را خودکار کامل می‌کند، چون کلاینت‌هایی مانند Mihomo Party و FlClash چنین گواهی‌ای را نمی‌پذیرند.

## دستورهای مدیریت

</div>

```text
m-ui                  interactive menu
m-ui start | stop | restart | status | logs
m-ui settings | configure | set-port
m-ui reset-credentials | reset-path
m-ui update | update-menu | ssl
m-ui enable | disable | clear-logs | uninstall
m-ui --lang <code>    menu language, e.g. m-ui --lang fa
```

<div dir="rtl">

منوی مدیریت از همان ۱۱ زبان پنل پشتیبانی می‌کند و انتخاب شما را به خاطر می‌سپارد. نصب‌کننده به انگلیسی باقی می‌ماند.

## مستندات

ویکی به زبان‌های انگلیسی و چینی ساده‌شده است:

* [Installation](https://github.com/RomanovCaesar/m-ui/wiki/Installation)، [Panel Configuration](https://github.com/RomanovCaesar/m-ui/wiki/Configuration)
* [Inbounds and Clients](https://github.com/RomanovCaesar/m-ui/wiki/Inbounds)، [Mihomo Settings](https://github.com/RomanovCaesar/m-ui/wiki/Mihomo-Settings)
* [Subscriptions](https://github.com/RomanovCaesar/m-ui/wiki/Subscriptions)، [Subscription Templates](https://github.com/RomanovCaesar/m-ui/wiki/Subscription-Templates)
* [Multi-control](https://github.com/RomanovCaesar/m-ui/wiki/Multi-control)، [Cloudflare WARP](https://github.com/RomanovCaesar/m-ui/wiki/Cloudflare-WARP)، [VPNGate](https://github.com/RomanovCaesar/m-ui/wiki/VPNGate)
* [Maintenance](https://github.com/RomanovCaesar/m-ui/wiki/Maintenance)، [API](https://github.com/RomanovCaesar/m-ui/wiki/API)، [Common Questions](https://github.com/RomanovCaesar/m-ui/wiki/Common-questions-and-problems)

## توسعه

اجرا از کد منبع در ویندوز؛ پنل در `http://127.0.0.1:2053` با حساب `admin` / `admin` باز می‌شود:

</div>

```powershell
.\scripts\run.ps1
```

<div dir="rtl">

ساخت فایل‌های اجرایی ویندوز و Linux amd64، یا همهٔ بسته‌های انتشار:

</div>

```powershell
.\scripts\build.ps1
.\scripts\build-release.ps1
```

<div dir="rtl">

با `go test ./...` و `go vet ./...` بررسی کنید. نسخه از `MUI_VERSION` یا تگ فعلی Git با `-X main.version=...` در برنامه قرار می‌گیرد. ساختار کد منبع و روند انتشار در [Development and Releases](https://github.com/RomanovCaesar/m-ui/wiki/Development) و پیاده‌سازی رابط کاربری در [`LIQUID-GLASS.md`](LIQUID-GLASS.md) آمده است.

## سپاسگزاری

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo): هستهٔ پروکسی و مدل پیکربندی
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui): تراکم اطلاعات و روند کار رابط کاربری
* [Sub-Store](https://github.com/sub-store-org/Sub-Store): رفتار مرجع برای قالب‌های اشتراک کلاینت‌ها
* [liquid-glass-webgl](https://github.com/martin65536/liquid-glass-webgl): کنترل‌های Liquid Glass (Apache-2.0، نگاه کنید به [`NOTICE`](NOTICE))

## مجوز

m-ui تحت مجوز [GNU General Public License version 3](LICENSE) منتشر می‌شود. کد تبدیل لینک Mihomo انتساب GPL-3.0 خود را در [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md) حفظ می‌کند.

</div>
