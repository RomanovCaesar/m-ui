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

Bảng điều khiển quản lý máy chủ dùng [Mihomo](https://github.com/MetaCubeX/mihomo) làm lõi. Cách thao tác theo 3x-ui, giao diện theo phong cách Liquid Glass của Apple, còn mô hình cấu hình là YAML gốc của Mihomo. Bảng điều khiển hỗ trợ 11 ngôn ngữ.

> [!IMPORTANT]
> Chỉ sử dụng dự án này trên máy chủ và mạng mà bạn sở hữu hoặc được phép quản trị. Hãy tuân thủ luật pháp sở tại, điều khoản của nhà cung cấp và chính sách của các dịch vụ bạn truy cập.

Tài liệu đầy đủ có tại [m-ui Wiki](https://github.com/RomanovCaesar/m-ui/wiki) (tiếng Anh). Theo dõi [kênh Telegram](https://t.me/MihomoUI) để nhận tin tức phát triển.

## Tính năng

**Bảng điều khiển và Mihomo**
* Trang tổng quan hiển thị trạng thái hệ thống, lưu lượng, kết nối và Mihomo; Mihomo tự khởi động cùng bảng điều khiển
* Tạo và kiểm tra YAML gốc của Mihomo, xem trước và tải xuống cấu hình
* Chuyển phiên bản Mihomo từ bản phát hành chính thức của MetaCubeX, cập nhật GeoIP / GeoSite / MetaDB
* Sao lưu và khôi phục đầy đủ: cài đặt, inbound, danh tính Multi-control (tùy chọn), bộ đệm liên bảng và mẫu đăng ký
* Giao diện tiếng Anh, tiếng Trung giản thể, tiếng Trung phồn thể, tiếng Nhật, tiếng Nga, tiếng Ba Tư, tiếng Việt, tiếng Tây Ban Nha, tiếng Thổ Nhĩ Kỳ, tiếng Ukraina và tiếng Bồ Đào Nha (Brazil)

**Inbound và client**
* Các listener `mixed`, `socks`, `http`, `shadowsocks`, `snell`, `vmess`, `vless`, `trojan`, `hysteria2`, `tuic`, `anytls`, `mieru`, `sudoku`, `shadowquic`, `trusttunnel` và `hysteria2-realm`
* Cài đặt TLS, Reality, ShadowTLS, RestTLS, TLSMirror, JLS, Trojan SS, XHTTP, mKCP, Mekya và che giấu lưu lượng
* Thông tin xác thực, hạn mức lưu lượng, thời hạn, lịch đặt lại và trạng thái trực tuyến cho từng client; có cả giới hạn ở cấp inbound
* Liên kết chia sẻ, mã QR, nhập inbound từ YAML listener của Mihomo, đặt lại lưu lượng hàng loạt

**Đăng ký**
* Trang đăng ký trên trình duyệt hiển thị mức dùng, hạn mức, thời hạn và các nút
* **Mẫu đăng ký:** mỗi đăng ký là một cấu hình client hoàn chỉnh, tạo từ mẫu tích hợp (Trung Quốc / Nga / Iran) hoặc mẫu Mihomo của riêng bạn
* Cấu hình đầy đủ cho Mihomo/Clash, Stash, sing-box, Surge, Surfboard, Loon, Quantumult X và Egern; danh sách nút cho V2Box, V2RayNG, Shadowrocket và các client dùng liên kết khác
* Token đăng ký riêng cho từng người dùng và cổng đăng ký riêng (tùy chọn)

**Cài đặt Mihomo**
* Basics: chế độ định tuyến, phiên bản IP cho kết nối trực tiếp, IPv6, TCP đồng thời, thống kê, nhật ký và định tuyến cơ bản
* Outbound (gồm Snell, WireGuard và OpenVPN) và nhóm chính sách, với biểu mẫu, YAML gốc và chuyển đổi liên kết
* Quy tắc định tuyến được xét từ trên xuống dưới
* Outbound Cloudflare WARP và lối ra IP dân dụng VPNGate theo quốc gia và nhà mạng

**Multi-control**
* Mạng P2P giữa các bảng điều khiển, không có nút chủ, dùng danh tính có chữ ký
* Đồng bộ inbound được mã hóa giữa các bảng điều khiển tin cậy
* Gộp đăng ký từ nhiều bảng điều khiển

## Cài đặt trên Linux

Chạy với quyền `root` trên Linux dùng systemd hoặc OpenRC:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

Trình cài đặt chọn một cổng trống có năm chữ số, tạo đường dẫn bảng điều khiển 16 ký tự, tên người dùng 14 ký tự và mật khẩu 16 ký tự. Địa chỉ truy cập và thông tin đăng nhập chỉ được in một lần khi kết thúc. Bạn cũng có thể tự chỉ định:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

m-ui được cài vào `/usr/local/m-ui`, dữ liệu nằm trong `/usr/local/m-ui/data`. Trình cài đặt cũng tải lõi Mihomo cho kiến trúc hiện tại; lõi đã có sẽ được giữ lại trừ khi bạn dùng `--update-mihomo`.

`m-ui update` nâng cấp m-ui và giữ nguyên mọi cài đặt. Trước khi cập nhật, nó chụp nhanh thư mục dữ liệu vào `data/backups/` và giữ 3 bản mới nhất.

Mỗi bản phát hành có `m-ui-linux-<architecture>.tar.gz` cho `386`, `amd64`, `arm64`, `armv5`, `armv6`, `armv7` và `s390x`, cùng với `m-ui-windows-amd64.zip`.

## Docker

Image Docker dành cho Linux amd64, chứa m-ui và một lõi Mihomo có phiên bản cố định, đã được kiểm tra. Compose dùng mạng của máy chủ, nên cổng của các inbound thêm trong bảng điều khiển hoạt động ngay mà không cần ánh xạ cổng.

```bash
git clone https://github.com/RomanovCaesar/m-ui.git
cd m-ui
cp .env.example .env
docker compose up -d --build
docker compose logs m-ui
```

Nếu `MUI_PASSWORD` và `MUI_PATH` để trống, lần khởi động đầu tiên sẽ tự tạo và in một lần địa chỉ cùng thông tin đăng nhập vào nhật ký. Mỗi bản phát hành cũng xuất bản `ghcr.io/romanovcaesar/m-ui:<tag>` và `latest`; nếu bạn dùng được image này, có thể chạy `docker compose pull` thay vì tự build. Dữ liệu nằm trong các volume `m-ui-data` và `m-ui-core`; đừng chạy `docker compose down -v` nếu không muốn xóa chúng.

## Chứng chỉ TLS

Lần cài đặt tương tác đầu tiên cho phép chọn chứng chỉ Let's Encrypt cho tên miền, chứng chỉ Let's Encrypt ngắn hạn cho IPv4, chứng chỉ có sẵn, hoặc bỏ qua TLS. Sau khi cài đặt, chạy:

```bash
m-ui ssl
```

Menu có thể cấp chứng chỉ cho tên miền và IP, dùng API DNS của Cloudflare để cấp chứng chỉ tên miền và wildcard khi cổng 80 không dùng được, gia hạn, thu hồi, liệt kê và áp dụng chứng chỉ có sẵn. Nếu tệp chứng chỉ chỉ có chứng chỉ lá, m-ui sẽ tự bổ sung chuỗi chứng chỉ, vì các client như Mihomo Party và FlClash từ chối loại chứng chỉ này.

## Lệnh quản lý

```text
m-ui                  interactive menu
m-ui start | stop | restart | status | logs
m-ui settings | configure | set-port
m-ui reset-credentials | reset-path
m-ui update | update-menu | ssl
m-ui enable | disable | clear-logs | uninstall
m-ui --lang <code>    menu language, e.g. m-ui --lang vi
```

Menu hỗ trợ 11 ngôn ngữ giống bảng điều khiển và ghi nhớ lựa chọn của bạn. Trình cài đặt vẫn dùng tiếng Anh.

## Tài liệu

Wiki có tiếng Anh và tiếng Trung giản thể:

* [Installation](https://github.com/RomanovCaesar/m-ui/wiki/Installation), [Panel Configuration](https://github.com/RomanovCaesar/m-ui/wiki/Configuration)
* [Inbounds and Clients](https://github.com/RomanovCaesar/m-ui/wiki/Inbounds), [Mihomo Settings](https://github.com/RomanovCaesar/m-ui/wiki/Mihomo-Settings)
* [Subscriptions](https://github.com/RomanovCaesar/m-ui/wiki/Subscriptions), [Subscription Templates](https://github.com/RomanovCaesar/m-ui/wiki/Subscription-Templates)
* [Multi-control](https://github.com/RomanovCaesar/m-ui/wiki/Multi-control), [Cloudflare WARP](https://github.com/RomanovCaesar/m-ui/wiki/Cloudflare-WARP), [VPNGate](https://github.com/RomanovCaesar/m-ui/wiki/VPNGate)
* [Maintenance](https://github.com/RomanovCaesar/m-ui/wiki/Maintenance), [API](https://github.com/RomanovCaesar/m-ui/wiki/API), [Common Questions](https://github.com/RomanovCaesar/m-ui/wiki/Common-questions-and-problems)

## Phát triển

Chạy từ mã nguồn trên Windows; bảng điều khiển mở tại `http://127.0.0.1:2053` với tài khoản `admin` / `admin`:

```powershell
.\scripts\run.ps1
```

Build tệp thực thi cho Windows và Linux amd64, hoặc toàn bộ gói phát hành:

```powershell
.\scripts\build.ps1
.\scripts\build-release.ps1
```

Kiểm tra bằng `go test ./...` và `go vet ./...`. Phiên bản được chèn bằng `-X main.version=...` từ `MUI_VERSION` hoặc tag Git hiện tại. Cấu trúc mã nguồn và quy trình phát hành xem tại [Development and Releases](https://github.com/RomanovCaesar/m-ui/wiki/Development), còn giao diện xem tại [`LIQUID-GLASS.md`](LIQUID-GLASS.md).

## Lời cảm ơn

* [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo): lõi proxy và mô hình cấu hình
* [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui): mật độ thông tin và cách thao tác của giao diện
* [Sub-Store](https://github.com/sub-store-org/Sub-Store): hành vi tham chiếu cho các định dạng đăng ký
* [liquid-glass-webgl](https://github.com/martin65536/liquid-glass-webgl): các điều khiển Liquid Glass (Apache-2.0, xem [`NOTICE`](NOTICE))

## Giấy phép

m-ui được phát hành theo [GNU General Public License version 3](LICENSE). Mã chuyển đổi liên kết của Mihomo giữ ghi nhận GPL-3.0 trong [`internal/mihomoconvert/NOTICE.md`](internal/mihomoconvert/NOTICE.md).
