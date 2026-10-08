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

Панель керування сервером на основі [Mihomo](https://github.com/MetaCubeX/mihomo). Логіка роботи повторює 3x-ui, інтерфейс виконано в стилі Apple Liquid Glass, а конфігурація будується в рідному YAML-форматі Mihomo. Панель доступна 11 мовами.

> [!IMPORTANT]
> Використовуйте проєкт лише на серверах і в мережах, якими ви володієте або якими вам дозволено керувати. Дотримуйтеся місцевого законодавства, умов провайдерів і правил сервісів, до яких ви звертаєтеся.

Повна документація — у [m-ui Wiki](https://github.com/RomanovCaesar/m-ui/wiki) (англійською). Новини розробки публікуються в нашому [Telegram-каналі](https://t.me/MihomoUI).

## Можливості

**Панель і Mihomo**
* Дашборд зі станом системи, трафіку, з'єднань і Mihomo; Mihomo запускається разом із панеллю
* Генерація та перевірка конфігурації в рідному YAML Mihomo, перегляд і завантаження підсумкового файлу
* Перемикання версій Mihomo з офіційних релізів MetaCubeX, оновлення GeoIP / GeoSite / MetaDB
* Повне резервне копіювання та відновлення: налаштування, входи, ідентичність Multi-control (за бажанням), кеш міжпанельних підписок і шаблони підписок
* Інтерфейс англійською, спрощеною та традиційною китайською, японською, російською, перською, в'єтнамською, іспанською, турецькою, українською та бразильською португальською

**Входи та клієнти**
* Слухачі `mixed`, `socks`, `http`, `shadowsocks`, `snell`, `vmess`, `vless`, `trojan`, `hysteria2`, `tuic`, `anytls`, `mieru`, `sudoku`, `shadowquic`, `trusttunnel` і `hysteria2-realm`
* Налаштування TLS, Reality, ShadowTLS, RestTLS, TLSMirror, JLS, Trojan SS, XHTTP, mKCP, Mekya та обфускації
* Облікові дані, ліміти трафіку, термін дії, періодичне скидання та онлайн-статус для кожного клієнта; ліміти й на рівні входу
* Посилання та QR-коди, імпорт входів із YAML-опису слухача Mihomo, масове скидання трафіку

**Підписки**
* Сторінка підписки в браузері: використання, ліміт, термін дії та вузли
* **Шаблони підписок:** кожна підписка — це повний профіль клієнта, зібраний із вбудованого шаблону (Китай / Росія / Іран) або вашого власного шаблону Mihomo
* Повні профілі для Mihomo/Clash, Stash, sing-box, Surge, Surfboard, Loon, Quantumult X і Egern; списки вузлів для V2Box, V2RayNG, Shadowrocket та інших клієнтів із посиланнями
* Окремий токен підписки для кожного користувача та необов'язковий окремий порт підписок

**Налаштування Mihomo**
* Basics: режим маршрутизації, версія IP для прямих з'єднань, IPv6, паралельний TCP, статистика, журнали та базова маршрутизація
* Вихідні (зокрема Snell, WireGuard та OpenVPN) і групи політик: форма, рідний YAML і перетворення посилань
* Правила маршрутизації, що перевіряються згори донизу
* Вихідні Cloudflare WARP і домашні IP через VPNGate з вибором країни та провайдера

**Multi-control**
* P2P-мережа панелей без головного вузла, з підписаними ідентичностями
* Зашифрована синхронізація входів між довіреними панелями
* Об'єднання підписок із кількох панелей

## Встановлення в Linux

Виконайте від імені `root` у Linux із systemd або OpenRC:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

Інсталятор обирає вільний п'ятизначний порт панелі та генерує шлях панелі з 16 символів, ім'я користувача з 14 символів і пароль із 16 символів. Адреса та облікові дані виводяться один раз наприкінці встановлення. Їх можна задати явно:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

m-ui встановлюється в `/usr/local/m-ui`, дані зберігаються в `/usr/local/m-ui/data`. Інсталятор також завантажує ядро Mihomo для поточної архітектури; наявне ядро зберігається, якщо не вказано `--update-mihomo`.

`m-ui update` оновлює m-ui зі збереженням усіх налаштувань. Перед оновленням створюється знімок каталогу даних у `data/backups/`; зберігаються три останні знімки.

У релізах є `m-ui-linux-<architecture>.tar.gz` для `386`, `amd64`, `arm64`, `armv5`, `armv6`, `armv7` і `s390x`, а також `m-ui-windows-amd64.zip`.

## Docker

Образ Docker зібрано для Linux amd64; він містить m-ui і перевірене ядро Mihomo фіксованої версії. Compose використовує мережу хоста, тому порти входів, доданих у панелі, працюють без окремого прокидання портів.

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

Якщо `MUI_PASSWORD` і `MUI_PATH` порожні, під час першого запуску їх буде згенеровано, а адресу та облікові дані один раз виведено в журнал. Кожен реліз також публікує `ghcr.io/romanovcaesar/m-ui:<tag>` і `latest`; якщо образ вам доступний, замість збирання можна виконати `docker compose pull`. Дані зберігаються в томах `m-ui-data` і `m-ui-core`; не запускайте `docker compose down -v`, якщо не хочете їх видалити.

## TLS-сертифікати

Під час першого інтерактивного встановлення можна обрати сертифікат Let's Encrypt для домену, короткостроковий сертифікат Let's Encrypt для IPv4, наявну пару сертифікат/ключ або пропустити TLS. Після встановлення виконайте:

```bash
m-ui ssl
```

У меню можна випустити сертифікати для домену та IP, отримати сертифікат для домену й wildcard через Cloudflare DNS API, якщо порт 80 недоступний, а також поновити, відкликати, переглянути й застосувати наявний сертифікат. Якщо файл сертифіката містить лише кінцевий сертифікат, m-ui сам доповнює ланцюжок: клієнти на кшталт Mihomo Party і FlClash такі сертифікати відхиляють.

## Команди керування

```text
m-ui                  interactive menu
m-ui start | stop | restart | status | logs
m-ui settings | configure | set-port
m-ui reset-credentials | reset-path
m-ui update | update-menu | ssl
m-ui enable | disable | clear-logs | uninstall
m-ui --lang <code>    menu language, e.g. m-ui --lang uk
```

Меню підтримує ті самі 11 мов, що й панель, і запам'ятовує вибір. Інсталятор залишається англійською.

## Документація

Wiki доступна англійською та спрощеною китайською:

* [Installation](https://github.com/RomanovCaesar/m-ui/wiki/Installation), [Panel Configuration](https://github.com/RomanovCaesar/m-ui/wiki/Configuration)
* [Inbounds and Clients](https://github.com/RomanovCaesar/m-ui/wiki/Inbounds), [Mihomo Settings](https://github.com/RomanovCaesar/m-ui/wiki/Mihomo-Settings)
* [Subscriptions](https://github.com/RomanovCaesar/m-ui/wiki/Subscriptions), [Subscription Templates](https://github.com/RomanovCaesar/m-ui/wiki/Subscription-Templates)
* [Multi-control](https://github.com/RomanovCaesar/m-ui/wiki/Multi-control), [Cloudflare WARP](https://github.com/RomanovCaesar/m-ui/wiki/Cloudflare-WARP), [VPNGate](https://github.com/RomanovCaesar/m-ui/wiki/VPNGate)
* [Maintenance](https://github.com/RomanovCaesar/m-ui/wiki/Maintenance), [API](https://github.com/RomanovCaesar/m-ui/wiki/API), [Common Questions](https://github.com/RomanovCaesar/m-ui/wiki/Common-questions-and-problems)

## Розробка

Запуск із вихідного коду у Windows; панель відкривається за адресою `http://127.0.0.1:2053`, обліковий запис `admin` / `admin`:

```powershell
.\scripts\run.ps1
```

Збирання бінарних файлів для Windows і Linux amd64 або всіх архівів релізу:

```powershell
.\scripts\build.ps1
.\scripts\build-release.ps1
```

Перевірка: `go test ./...` і `go vet ./...`. Версія вбудовується через `-X main.version=...` з `MUI_VERSION` або поточного тегу Git. Структуру вихідного коду та процес релізу описано в [Development and Releases](https://github.com/RomanovCaesar/m-ui/wiki/Development), інтерфейс — у [`LIQUID-GLASS.md`](LIQUID-GLASS.md).

## Подяки

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo): проксі-ядро та модель конфігурації
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui): щільність інтерфейсу та логіка роботи
* [Sub-Store](https://github.com/sub-store-org/Sub-Store): еталонна поведінка форматів підписок для клієнтів
* [liquid-glass-webgl](https://github.com/martin65536/liquid-glass-webgl): елементи керування Liquid Glass (Apache-2.0, див. [`NOTICE`](NOTICE))

## Ліцензія

m-ui поширюється за ліцензією [GNU General Public License version 3](LICENSE). Код перетворення посилань Mihomo зберігає вказівку на GPL-3.0 у [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md).
