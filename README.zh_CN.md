[English](/README.md) | [中文](/README.zh_CN.md)

# m-ui

[![Release](https://img.shields.io/github/v/release/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/releases)
[![Build](https://img.shields.io/github/actions/workflow/status/RomanovCaesar/m-ui/release.yml.svg)](https://github.com/RomanovCaesar/m-ui/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/RomanovCaesar/m-ui.svg)](https://github.com/RomanovCaesar/m-ui/blob/main/go.mod)
[![Downloads](https://img.shields.io/github/downloads/RomanovCaesar/m-ui/total.svg)](https://github.com/RomanovCaesar/m-ui/releases/latest)

一个使用 Mihomo 作为代理内核的服务端管理面板，界面设计参考 3x-ui 2.9.3 的信息密度和工作流。

> [!IMPORTANT]
> 本项目只应用于您拥有或获授权管理的服务器和网络。请遵守所在地法律、上游服务商条款以及您所访问服务的使用政策。

完整使用说明请参阅 [m-ui Wiki](https://github.com/RomanovCaesar/m-ui/wiki)。

## Linux 安装

在使用 systemd 或 OpenRC 的 Linux 上以 root 执行：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh)
```

没有指定时，安装器会随机选择一个未占用的五位数面板端口，并生成 16 位面板路径、14 位账号和 16 位密码。生成的字符串均同时包含大写字母、小写字母和数字。手动指定端口时允许 `1-65535`，手动指定路径时可使用 m-ui 支持的 URL-safe 多段路径或 `/` 根路径。安装完成后只在终端打印一次最终账号、密码和访问地址。

也可以显式指定：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/RomanovCaesar/m-ui/main/install.sh) \
  --port 34567 \
  --path MyPanelA1b2C3d4 \
  --username AdminUserA1234 \
  --password 'ChangeThisA12345'
```

程序安装在 `/usr/local/m-ui`，持久数据位于 `/usr/local/m-ui/data`。重复执行安装器会升级程序并保留现有设置。安装器还会下载适配当前架构的 Mihomo 内核；已有内核默认保留，可用 `--update-mihomo` 更新，或用 `--skip-mihomo` 跳过。m-ui 的 GitHub Release 只需发布 Linux 资产，文件名必须是 `m-ui-linux-<architecture>.tar.gz`，支持 `386`、`amd64`、`arm64`、`armv5`、`armv6`、`armv7` 和 `s390x`；不需要 Windows 资产。tar.gz 内应包含对应的 `m-ui-linux-<architecture>` 或 `m-ui` 可执行文件。

首次安装会提供 TLS 证书设置菜单，功能包括域名 Let’s Encrypt 证书、IPv4 Let’s Encrypt short-lived 证书，以及手动填写已有证书和私钥。域名/IP 证书使用 `/root/.acme.sh` 和 standalone HTTP-01，证书安装到 `/root/cert/` 并配置为 m-ui 的面板证书，续期时自动重启面板。也可以使用 `--ssl-domain DOMAIN`、`--ssl-ip IP` 或 `--cert FILE --key FILE` 非交互配置；HTTP-01 默认需要外部 TCP 80 端口可达。

已安装面板执行 `m-ui ssl` 会进入完整证书管理菜单，支持申请域名证书、申请 IP 短期证书、使用 Cloudflare DNS API 申请域名与通配符证书、强制续期、吊销、查看证书，以及将已有证书路径套用到面板。Cloudflare 模式支持 API Token，或 Global API Key 加账号邮箱；密钥输入不会回显。

安装完成后可使用：

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

## 运行

在 `m-ui` 目录执行：

```powershell
.\scripts\run.ps1
```

浏览器打开 `http://127.0.0.1:2053`，默认账号为 `admin`，密码为 `admin`。

项目会优先使用配置中的 Mihomo 路径。首次运行默认查找上级目录的 `mihomo.exe`（Windows）或 `mihomo`（Linux）。配置文件和面板状态保存在 `m-ui/data`，不会修改旁边已有的源码目录和内核文件。

构建 Windows 可执行文件：

```powershell
.\scripts\build.ps1
```

构建 GitHub Release 使用的全部 Linux 压缩包：

```powershell
.\scripts\build-release.ps1
```

构建脚本优先使用环境变量 `MUI_VERSION`，否则读取当前提交的精确 Git tag；当前提交没有 tag 时生成 `dev-<commit>`。Release 自动化应将 GitHub tag 传入，例如 `MUI_VERSION=v0.2.0`。版本通过 `-X main.version=...` 写入二进制，因此 `m-ui version`、面板状态和备份清单都会显示实际 Release 版本，不再依赖源码中的固定版本号。

源码构建使用 Go modules；YAML 导入依赖 `gopkg.in/yaml.v3`。Windows 脚本将模块缓存保存在项目的 `.gomodcache`，构建缓存保存在 `.gocache`。

## 当前能力

- 3x-ui 风格的登录页、侧栏、仪表盘、流量统计和响应式表格
- Mihomo mixed、socks、http、shadowsocks、snell、vmess、vless、trojan、hysteria2、tuic、anytls、mieru、sudoku、shadowquic、trusttunnel 和 hysteria2-realm listeners
- Trojan 支持 Mihomo 的 JLS、Trojan SS 和 Reality 回落限速选项
- Mixed/HTTP/Socks 支持 Mihomo 原生用户名密码列表；Shadowsocks 和 SS2022 统一使用启用客户端的密码及 Mihomo 原生加密方式
- Mihomo Shadowsocks listener 本身只有一个核心密码；多个 SS 客户端密码仅作为分享链接/面板元数据保留，不能由 Mihomo 核心单独强制校验
- 生成并校验原生 YAML 格式的 Mihomo `config.yaml`
- 启动、停止、重启 Mihomo，读取日志和 REST 控制器状态
- 首页系统仪表盘、运行配置查看/复制/下载、数据备份恢复
- 公开订阅页、Base64、Mihomo/Clash、Sing-box、Surge、Surfboard、Loon、Quantumult X、Stash、Egern 等订阅转换；可选独立订阅服务端口，留空则沿用面板端口
- 从 MetaCubeX 官方 GitHub Releases 下载和切换 Mihomo 版本
- 更新官方 GeoIP、GeoSite 和 MetaDB 文件
- 修改核心路径、API 端口、密钥、代理端口、运行模式、面板 TLS 证书路径和登录密码
- Mihomo Settings：管理 Outbounds（代理节点/策略组）和自上而下匹配的 Routing Rules；支持 Cloudflare WARP 设备注册、账户信息、WARP+ License Key 和 WireGuard 出站。
- Basics 默认置顶，仅 General 初始展开；支持运行模式、直连 IP 版本、IPv6、TCP 并发、统一延迟、测速 URL、统计采样/落盘间隔、出站流量统计和面板日志选项。
- Basic Routing 优先级为阻断 IP/域名、IPv4 Routing、WARP Routing、自定义规则；会自动生成所需的直连策略。Reset 只恢复 Basics 草稿，不删除节点、规则和历史流量。
- Outbound 的 Form / YAML 编辑器支持原生 YAML 和分享链接导入；YAML 可包含单个节点、数组或 `proxies` / `proxy-groups`，扩展字段随配置保存。点击 Link 右侧图标将分享链接转为 YAML，Apply to Draft 后仍需 Save 并重启 Mihomo。
- Panel Settings 的 Multi-control 支持多个 m-ui 面板对等联机。任一节点配置 16 至 32 位配对 token（支持手动输入或生成 16 位 token），其他面板输入该节点的 IP / 域名、面板端口和 token 即可加入。
- 支持通过加密 P2P 通道向其他在线面板同步选定的 Inbound 与 Client。
- 支持拉取、缓存并聚合所有已配对面板的节点信息，按 Username 输出跨面板订阅页面和 Clash 地址。
- Mihomo TLS 证书支持文件路径或 PEM 内容；支持 mTLS、ECH 私钥和从面板 TLS 设置自动填充证书
- 原始配置预览，支持复制到剪贴板

默认入口端口为 `12080`。如果端口已被占用，可在面板设置和入口编辑器中修改。

## 源码结构

- `main.go`：程序启动入口
- `internal/app/`：面板后端、Mihomo 管理、订阅、多面板联机及相应 Go 测试
- `internal/mihomoconvert/`：Mihomo 分享链接转换
- `web/`：嵌入二进制的前端页面、样式与脚本
- `tests/`：浏览器端 JavaScript 回归测试
- `scripts/`：本地运行、构建、Release 打包和 Linux 管理脚本
- `deploy/`：systemd 与 OpenRC 服务定义
- `install.sh`：GitHub 一键安装入口

`deploy/m-ui.service` 和 `deploy/m-ui.openrc` 默认假定面板安装在 `/usr/local/m-ui`；安装器使用自定义安装目录时会自动替换路径。

分享链接转换复用本地 Mihomo 的 `common/convert`，来源和 GPL-3.0 许可见 `internal/mihomoconvert/NOTICE.md`。解析链接不访问远程订阅地址。

### Cloudflare WARP

Outbounds 和 Basics 的 WARP 按钮共用账户窗口。Create 会接受 Cloudflare WARP 服务条款并注册设备，私钥在服务器本地生成，仅公钥发送给 Cloudflare。账户记录保存在 `data/state.json`，并包含在面板备份中。

Add Outbound 将账户的 endpoint、隧道地址、peer public key 和 client_id 转成 Mihomo `wireguard` 节点，加入当前草稿。此时 Basics 的 WARP Routing 变为域名标签输入框，支持域名、`full:`、`keyword:` 和 `geosite:`。点击 Save 并 Restart Mihomo 后应用；不更改系统默认路由，也不依赖系统 WARP 客户端。

More Information 从 Cloudflare 刷新设备资料；License Key 更新使用已有设备的账户接口。Reset Outbound 从最新账户资料重建节点并保留其名称和引用。Delete 与本地 3x-ui 的行为一致：仅清除面板账户记录，将 WARP 出站和 Basics WARP 规则的删除加入草稿，不向 Cloudflare 注销设备；存在其他规则或策略组引用时须先移除引用。

注册使用与本地 3x-ui 相同的 consumer API `api.cloudflareclient.com/v0a2158`。其可用性取决于 Cloudflare 和节点网络；界面会显示超时、HTTP 错误和缺失的 WireGuard 配置。测试通过本地模拟 API 验证协议和状态流转，不会自动创建真实设备。

Mihomo 不提供 Xray 的 BitTorrent 协议匹配，相关开关为禁用状态；DNS 诊断跟随 Debug 日志级别。入站/客户端配额统计固定开启，出站统计按 `/connections` 采样累加，短于采样间隔的连接可能不包含在分类计数中。Basic Routing 与 Routing Rules 在 Rule 模式生效；入口显式指定 `proxy` 时遵循 Mihomo 的入口出口优先规则。

### Multi-control

Panel Settings → Subscription 的 `Cross Panel Subscription Path` 独立于普通订阅路径。首次启动或升级旧数据时自动生成 `/isub` 加 16 位小写字母和数字混合 token，之后重启保持不变。手动填写也必须使用 `/isub` 加 16 位小写字母和数字的格式，或点击输入框右侧按钮重新生成；点击顶部 Save 后保存。跨面板订阅页面使用 `https://example.com:port/<cross-sub-path>/<Username Token>`，直接 Clash 订阅使用同一地址追加 `/clash`。从 Inbounds → General Actions → `Export All Subscriptions (Cross Panel)` 可以拉取全部已配对面板的 Inbound 信息、查看进度、使用远端缓存并导出聚合地址。

每台面板首次启动会在 `data/multi-control.json` 生成独立的 Ed25519 身份。配对 token 只用于首次双向认证；配对后，各节点使用签名身份心跳并交换已签名的节点地址。新成员会自动发现现有成员并尝试直接连接，因此介绍节点离线不会使其余可互访的节点失去联机。离线节点保留在成员列表并退避重试，当前单个网络最多保存 128 个节点。

使用前需要在 Multi-control 的 Local Node 中填写其他服务器实际可访问的本机地址，例如 `https://panel-a.example.com:2053`。连接时输入任意已开启配对 token 的成员地址。面板配置了 HTTPS 时必须使用证书对应的域名；不会跳过证书校验。NAT、防火墙和云安全组仍需允许服务器之间访问面板端口，目前不包含 NAT 穿透或中继。

面板隐藏 URI 路径不会参与服务器握手；对等协议固定使用面板端口的 `/_m-ui/peer/v1`，订阅专用端口不提供该接口。管理、生成 token 和查看成员仍需登录面板。配对 token 和私钥不通过普通状态接口或对等网络传播。

“断开”是本机决定：该节点会从当前面板删除并加入本机阻止列表，避免被其他节点重新发现，但不会从整个网络中全局删除。允许重新发现后，可通过仍在线的共同成员恢复连接。

`multi-control.json` 不包含在可导入到其他服务器的面板 ZIP 备份中，避免克隆出两个相同的节点身份。迁移同一台服务器时需要单独、安全地迁移该文件；复制为新服务器时不要复制它。

### Sync Inbound

在 Inbounds → General Actions → Sync Inbound 打开同步窗口。左侧列出当前在线的其他面板并默认全选；右侧按本机 Username × Inbound 显示实际 Client 关系。绿色为选中、白色为未选中，不存在的组合为灰色禁用。可以按整行、整列或单个格子选择；SS、Snell、Sudoku 等单 Client 入站仅对应一个有效格子。无 Client 的透明代理或无认证入站不在本次按 Username 同步的选择范围内。

点击同步后需要再次确认，取消会保留原选择。确认后由本机后台任务向所选面板直接传输，进度与结果按面板和入站显示；关闭窗口不会取消已提交任务，任务仍在运行时再次点击 Sync Inbound 可继续查看。每次最多选择 256 个入站、4096 个 Client、128 台目标，配置内容上限 4 MiB，最多同时处理 4 台目标。任务记录保留最近 20 次，重启面板后清空；已同步配置永久保存在目标数据中。

目标以发送节点身份和来源入站 ID 识别副本：首次新增，重复同步更新所选 Client 及该入站的公共配置，保留目标其余 Client、现有流量统计、在线记录和订阅 token。目标同名/同端口的其他入站或面板服务端口会报告冲突，不覆盖本地入站；某个入站失败不妨碍其他有效入站保存。结果中的“成功”表示已保存目标入站及生成 YAML，需要在目标面板重启 Mihomo 后生效。

两端都需升级到支持 Sync Inbound 的版本。传输使用面板端口 `/_m-ui/peer/inbound/v1`，与成员心跳接口分离；反向代理需转发此路径，专用订阅端口不提供此接口。已配对的身份签名认证临时 X25519 密钥，入站与结果使用 AES-GCM 加密，因此 HTTP 面板之间也不明文传输 Client 密码、UUID 和私钥。仅已信任且未断开的节点可同步；消息有时间窗口、单次会话和重放校验。

入站引用的证书、私钥和客户端 CA 文件会在来源面板读取并转为 PEM 内容随入站传递，不读取目标的同名路径。Listen IP、伪装目标、路由出站等机器相关配置沿用来源设置；目标需具备相应网络环境与出站配置。Sync Inbound 负责主动下发所选入站；跨面板订阅聚合见下方说明。

面板 TLS 证书配置后需要重启 m-ui 才会切换到 HTTPS。证书签发和续期由安装/管理脚本中的 acme.sh 或用户自己的证书工具负责，m-ui 读取已配置的证书路径。

### Cross-panel subscription aggregation

在 Inbounds → General Actions → `Export All Subscriptions (Cross Panel)` 打开聚合窗口。点击“拉取全体 Inbound 信息”后，面板会通过已配对的 P2P 节点逐台读取入站和 Username token，显示拉取进度，并把结果按来源节点缓存到 `data/cross-panel-subscriptions.json`。网络暂时失败时保留该节点的上一份缓存；“清除远端缓存”才会删除所有非本机缓存。本机入站始终直接读取当前状态，不依赖缓存。

窗口中的地址按 Username 生成裸跨面板订阅页面地址，例如 `https://example.com:port/isub.../<Username Token>`。地址页面包含聚合后的节点列表，节点按 Inbound Name 排序；页面中的 Clash 地址会在同一地址后追加 `/clash`。远端节点和 token 只通过已配对节点的签名、加密 P2P 通道传输；只存在于远端的 Username 也会使用远端缓存中的 token 出现在地址列表中。
