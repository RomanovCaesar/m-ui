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

Панель управления сервером на базе [Mihomo](https://github.com/MetaCubeX/mihomo). Логика работы повторяет 3x-ui, интерфейс выполнен в стиле Apple Liquid Glass, а конфигурация строится в родном YAML-формате Mihomo. Панель доступна на 11 языках.

> [!IMPORTANT]
> Используйте проект только на серверах и в сетях, которыми вы владеете или которыми вам разрешено управлять. Соблюдайте местное законодательство, условия провайдеров и правила сервисов, к которым вы обращаетесь.

Полная документация находится в [m-ui Wiki](https://github.com/RomanovCaesar/m-ui/wiki) (на английском). Новости разработки публикуются в нашем [Telegram-канале](https://t.me/MihomoUI).

## Возможности

**Панель и Mihomo**
* Дашборд с состоянием системы, трафика, соединений и Mihomo; Mihomo запускается вместе с панелью
* Генерация и проверка конфигурации в родном YAML Mihomo, просмотр и скачивание итогового файла
* Переключение версий Mihomo из официальных релизов MetaCubeX, обновление GeoIP / GeoSite / MetaDB
* Полное резервное копирование и восстановление: настройки, входы, идентичность Multi-control (по желанию), кэш межпанельных подписок и шаблоны подписок
* Интерфейс на английском, упрощённом и традиционном китайском, японском, русском, персидском, вьетнамском, испанском, турецком, украинском и бразильском португальском

**Входы и клиенты**
* Слушатели `mixed`, `socks`, `http`, `shadowsocks`, `snell`, `vmess`, `vless`, `trojan`, `hysteria2`, `tuic`, `anytls`, `mieru`, `sudoku`, `shadowquic`, `trusttunnel` и `hysteria2-realm`
* Настройки TLS, Reality, ShadowTLS, RestTLS, TLSMirror, JLS, Trojan SS, XHTTP, mKCP, Mekya и обфускации
* Учётные данные, лимиты трафика, срок действия, периодический сброс и онлайн-статус для каждого клиента; лимиты и на уровне входа
* Ссылки и QR-коды, импорт входов из YAML-описания слушателя Mihomo, массовый сброс трафика

**Подписки**
* Страница подписки в браузере: расход, лимит, срок действия и узлы
* **Шаблоны подписок:** каждая подписка — это полный профиль клиента, собранный из встроенного шаблона (Китай / Россия / Иран) или вашего собственного шаблона Mihomo
* Полные профили для Mihomo/Clash, Stash, sing-box, Surge, Surfboard, Loon, Quantumult X и Egern; списки узлов для V2Box, V2RayNG, Shadowrocket и других клиентов со ссылками
* Отдельный токен подписки для каждого пользователя и необязательный отдельный порт подписок

**Настройки Mihomo**
* Basics: режим маршрутизации, версия IP для прямых соединений, IPv6, параллельный TCP, статистика, журналы и базовая маршрутизация
* Исходящие (включая Snell, WireGuard и OpenVPN) и группы политик: форма, родной YAML и преобразование ссылок
* Правила маршрутизации, проверяемые сверху вниз
* Исходящие Cloudflare WARP и домашние IP через VPNGate с выбором страны и провайдера

**Multi-control**
* P2P-сеть панелей без главного узла, с подписанными идентичностями
* Зашифрованная синхронизация входов между доверенными панелями
* Объединение подписок с нескольких панелей

## Установка в Linux

Выполните от имени `root` в Linux с systemd или OpenRC:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

Установщик выбирает свободный пятизначный порт панели и генерирует путь панели из 16 символов, имя пользователя из 14 символов и пароль из 16 символов. Адрес и учётные данные выводятся один раз в конце установки. Их можно задать явно:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

m-ui устанавливается в `/usr/local/m-ui`, данные хранятся в `/usr/local/m-ui/data`. Установщик также скачивает ядро Mihomo для текущей архитектуры; уже установленное ядро сохраняется, если не указан `--update-mihomo`.

`m-ui update` обновляет m-ui с сохранением всех настроек. Перед обновлением создаётся снимок каталога данных в `data/backups/`; хранятся три последних снимка.

В релизах есть `m-ui-linux-<architecture>.tar.gz` для `386`, `amd64`, `arm64`, `armv5`, `armv6`, `armv7` и `s390x`, а также `m-ui-windows-amd64.zip`.

## Docker

Образ Docker собран для Linux amd64 и содержит m-ui и проверенное ядро Mihomo фиксированной версии. Compose использует сеть хоста, поэтому порты входов, добавленных в панели, работают без отдельного проброса портов.

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

Если `MUI_PASSWORD` и `MUI_PATH` пусты, при первом запуске они генерируются, а адрес и учётные данные один раз выводятся в журнал. Каждый релиз также публикует `ghcr.io/romanovcaesar/m-ui:<tag>` и `latest`; если образ вам доступен, вместо сборки можно выполнить `docker compose pull`. Данные хранятся в томах `m-ui-data` и `m-ui-core`; не запускайте `docker compose down -v`, если не хотите их удалить.

## TLS-сертификаты

При первой интерактивной установке можно выбрать сертификат Let's Encrypt для домена, краткосрочный сертификат Let's Encrypt для IPv4, существующую пару сертификат/ключ или пропустить TLS. После установки выполните:

```bash
m-ui ssl
```

В меню можно выпустить сертификаты для домена и IP, получить сертификат для домена и wildcard через Cloudflare DNS API, если порт 80 недоступен, а также продлить, отозвать, посмотреть и применить существующий сертификат. Если файл сертификата содержит только конечный сертификат, m-ui сам дополняет цепочку: клиенты вроде Mihomo Party и FlClash такие сертификаты отклоняют.

## Команды управления

```text
m-ui                  interactive menu
m-ui start | stop | restart | status | logs
m-ui settings | configure | set-port
m-ui reset-credentials | reset-path
m-ui update | update-menu | ssl
m-ui enable | disable | clear-logs | uninstall
m-ui --lang <code>    menu language, e.g. m-ui --lang ru
```

Меню поддерживает те же 11 языков, что и панель, и запоминает выбор. Установщик остаётся на английском.

## Документация

Wiki доступна на английском и упрощённом китайском:

* [Installation](https://github.com/RomanovCaesar/m-ui/wiki/Installation), [Panel Configuration](https://github.com/RomanovCaesar/m-ui/wiki/Configuration)
* [Inbounds and Clients](https://github.com/RomanovCaesar/m-ui/wiki/Inbounds), [Mihomo Settings](https://github.com/RomanovCaesar/m-ui/wiki/Mihomo-Settings)
* [Subscriptions](https://github.com/RomanovCaesar/m-ui/wiki/Subscriptions), [Subscription Templates](https://github.com/RomanovCaesar/m-ui/wiki/Subscription-Templates)
* [Multi-control](https://github.com/RomanovCaesar/m-ui/wiki/Multi-control), [Cloudflare WARP](https://github.com/RomanovCaesar/m-ui/wiki/Cloudflare-WARP), [VPNGate](https://github.com/RomanovCaesar/m-ui/wiki/VPNGate)
* [Maintenance](https://github.com/RomanovCaesar/m-ui/wiki/Maintenance), [API](https://github.com/RomanovCaesar/m-ui/wiki/API), [Common Questions](https://github.com/RomanovCaesar/m-ui/wiki/Common-questions-and-problems)

## Разработка

Запуск из исходников в Windows; панель открывается по адресу `http://127.0.0.1:2053`, учётная запись `admin` / `admin`:

```powershell
.\scripts\run.ps1
```

Сборка бинарных файлов для Windows и Linux amd64 или всех архивов релиза:

```powershell
.\scripts\build.ps1
.\scripts\build-release.ps1
```

Проверка: `go test ./...` и `go vet ./...`. Версия внедряется через `-X main.version=...` из `MUI_VERSION` или текущего тега Git. Структура исходников и процесс релиза описаны в [Development and Releases](https://github.com/RomanovCaesar/m-ui/wiki/Development), интерфейс — в [`LIQUID-GLASS.md`](LIQUID-GLASS.md).

## Благодарности

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo): прокси-ядро и модель конфигурации
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui): плотность интерфейса и логика работы
* [Sub-Store](https://github.com/sub-store-org/Sub-Store): эталонное поведение форматов подписок для клиентов
* [liquid-glass-webgl](https://github.com/martin65536/liquid-glass-webgl): элементы управления Liquid Glass (Apache-2.0, см. [`NOTICE`](NOTICE))

## Лицензия

m-ui распространяется по лицензии [GNU General Public License version 3](LICENSE). Код преобразования ссылок Mihomo сохраняет указание на GPL-3.0 в [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md).
