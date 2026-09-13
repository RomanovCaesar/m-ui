package app

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// 服务端提示的中英对照。前端整页都接了 i18n，但 API 的失败提示是直接 toast
// apiResponse.Message 的，如果这里还是中文，英文界面就会露出半句中文，所以在写
// 响应的出口把文案翻一次。
//
// translateMessage 只做三步，命不中就退回中文——宁可少翻，也不要翻出半截乱码：
//  1. 整句命中 messagesEN 就直接换掉；
//  2. 换不掉就按 ": " 拆开（fmt.Errorf("...: %w") 包出来的层级、以及把值放在
//     末尾的提示都是这个形状），逐段翻译后再用 ": " 拼回去，认不出的段落原样保留；
//  3. 段落本身带参数（端口号、名字、协议名）时交给 messagePatternsEN 的正则。
func settingsLanguage(s Settings) string {
	return normalizeLanguage(s.Language)
}

// normalizeLanguage 把设置里的界面语言收敛到 supportedLanguages，空值和非法值都
// 退回 defaultLanguage。
func normalizeLanguage(raw string) string {
	if language := strings.TrimSpace(raw); containsString(supportedLanguages, language) {
		return language
	}
	return defaultLanguage
}

// lang 读当前界面语言。走 mu 是因为设置随时可能被另一个请求改掉，而调用点都在
// 已经离开 CoreManager 方法的 handler 里，不会和内部锁打架。
func (a *App) lang() string {
	if a == nil || a.manager == nil {
		return defaultLanguage
	}
	return a.manager.lang()
}

func (m *CoreManager) lang() string {
	if m == nil {
		return defaultLanguage
	}
	m.mu.Lock()
	language := m.state.Settings.Language
	m.mu.Unlock()
	return normalizeLanguage(language)
}

func (m *CoreManager) updateLanguage(raw string) error {
	language := strings.TrimSpace(raw)
	if !containsString(supportedLanguages, language) {
		return fmt.Errorf("界面语言无效")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Settings.Language == language {
		return nil
	}
	previous := m.state
	m.state.Settings.Language = language
	if err := m.saveLocked(); err != nil {
		m.state = previous
		return err
	}
	return nil
}

// tr 按当前界面语言翻译一条给用户看的提示。
func (a *App) tr(text string) string {
	return translateMessage(text, a.lang())
}

// tr 给少数拿不到 *App 的 CoreManager 流程（例如导出备份）用，语义与 App.tr 一致。
func (m *CoreManager) tr(text string) string {
	return translateMessage(text, m.lang())
}

// trPeerView / trSyncJob / trCrossTargets 翻译那些先落进状态、之后才随接口吐给前端
// 的提示。落盘的仍是中文原文，等出面板时再按当前语言翻，所以切换语言后重新拉一次
// 同一个任务，文案也会跟着换。
func (a *App) trPeerView(view peerView) peerView {
	language := a.lang()
	view.Peers = slices.Clone(view.Peers)
	for i := range view.Peers {
		view.Peers[i].Error = translateMessage(view.Peers[i].Error, language)
	}
	return view
}

func (a *App) trSyncJob(job *inboundSyncJob) *inboundSyncJob {
	if job == nil {
		return nil
	}
	language := a.lang()
	translated := *job
	translated.Targets = slices.Clone(job.Targets)
	for i := range translated.Targets {
		target := &translated.Targets[i]
		target.Message = translateMessage(target.Message, language)
		target.Results = slices.Clone(target.Results)
		for j := range target.Results {
			target.Results[j].Message = translateMessage(target.Results[j].Message, language)
		}
	}
	return &translated
}

// translateMessage 把中文提示翻成目标语言。zh-CN 是原文，直接返回。
func translateMessage(text, language string) string {
	if language != "en" || text == "" || !hasCJK(text) {
		return text
	}
	if translated, ok := translateSegment(text); ok {
		return translated
	}
	parts := strings.Split(text, ": ")
	changed := false
	for i, part := range parts {
		if translated, ok := translateSegment(part); ok {
			parts[i] = translated
			changed = true
		}
	}
	if !changed {
		return text
	}
	return strings.Join(parts, ": ")
}

// translateSegment 翻一段（整句、或者 ": " 拆出来的一层），翻不了就报 false，让
// 调用方保留原文。
func translateSegment(segment string) (string, bool) {
	if translated, ok := messagesEN[segment]; ok {
		return translated, true
	}
	if !hasCJK(segment) {
		return segment, false
	}
	for _, pattern := range messagePatternsEN {
		if match := pattern.from.FindStringSubmatchIndex(segment); match != nil {
			return string(pattern.from.ExpandString(nil, pattern.to, segment, match)), true
		}
	}
	return segment, false
}

func hasCJK(text string) bool {
	for _, r := range text {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

// messagePatternsEN 覆盖带参数的提示：参数在中间时整句对不上表，只能用正则把值
// 抠出来重新拼。顺序有意义，带具体中文标签的规则必须排在通用规则前面。
var messagePatternsEN = []struct {
	from *regexp.Regexp
	to   string
}{
	{regexp.MustCompile(`^此用户没有 (.+) 可用节点$`), "This user has no available $1 nodes"},
	{regexp.MustCompile(`^跨面板订阅路径不能使用保留路径 /(.+)$`), "The cross-panel subscription path cannot use the reserved path /$1"},
	{regexp.MustCompile(`^订阅路径不能使用面板保留路径 /(.+)$`), "The subscription path cannot use the panel's reserved path /$1"},
	{regexp.MustCompile(`^GitHub 返回状态 (\d+)$`), "GitHub returned status $1"},
	{regexp.MustCompile(`^GitHub 找不到版本 (.+)$`), "GitHub has no release $1"},
	{regexp.MustCompile(`^版本 (\S+) 没有适合 (\S+) 的兼容构建$`), "Release $1 has no build for $2"},
	{regexp.MustCompile(`^下载 (\S+) 失败：GitHub 与 jsDelivr 均不可用，请检查网络或 HTTPS_PROXY$`), "Failed to download $1: neither GitHub nor jsDelivr is reachable — check the network or HTTPS_PROXY"},
	{regexp.MustCompile(`^下载服务器返回状态 (\d+)$`), "The download server returned status $1"},
	{regexp.MustCompile(`^Mihomo 已切换到 (.+)$`), "Mihomo switched to $1"},
	{regexp.MustCompile(`^会话有效期不能小于 (\d+) 分钟$`), "The session lifetime cannot be shorter than $1 minutes"},
	{regexp.MustCompile(`^分页大小必须在 0-(\d+) 之间$`), "The page size must be between 0 and $1"},
	{regexp.MustCompile(`^订阅服务端口 (\d+) 已被面板监听地址 (\S+)占用$`), "Subscription service port $1 is already used by the panel listen address $2"},
	{regexp.MustCompile(`^订阅服务端口 (\d+) 已被Mihomo API 地址 (\S+)占用$`), "Subscription service port $1 is already used by the Mihomo API address $2"},
	{regexp.MustCompile(`^订阅服务端口 (\d+) 已被入口 (.+) 使用$`), "Subscription service port $1 is already used by inbound $2"},
	{regexp.MustCompile(`^入口名称 (.+) 已被占用，Mihomo 会因 listener 重名拒绝整份配置$`), "Inbound name $1 is already taken — Mihomo rejects the whole config on duplicate listener names"},
	{regexp.MustCompile(`^入口名称 (.+) 重复，Mihomo 会拒绝整份配置，请先改名$`), "Duplicate inbound name $1 — Mihomo rejects the whole config, rename it first"},
	{regexp.MustCompile(`^端口 (\d+) 已被入口 (.+) 使用$`), "Port $1 is already used by inbound $2"},
	{regexp.MustCompile(`^端口 (\d+) 已被面板监听地址 (\S+)占用$`), "Port $1 is already used by the panel listen address $2"},
	{regexp.MustCompile(`^端口 (\d+) 已被Mihomo API 地址 (\S+)占用$`), "Port $1 is already used by the Mihomo API address $2"},
	{regexp.MustCompile(`^端口 (\d+) 已被订阅服务监听地址 (\S+)占用$`), "Port $1 is already used by the subscription service listen address $2"},
	{regexp.MustCompile(`^端口 (\d+) 已被目标入站 (.+) 占用$`), "Port $1 is already used by target inbound $2"},
	{regexp.MustCompile(`^端口 (\d+) 与面板监听地址 (\S+)冲突$`), "Port $1 conflicts with the panel listen address $2"},
	{regexp.MustCompile(`^端口 (\d+) 与Mihomo API 地址 (\S+)冲突$`), "Port $1 conflicts with the Mihomo API address $2"},
	{regexp.MustCompile(`^端口 (\d+) 与订阅服务监听地址 (\S+)冲突$`), "Port $1 conflicts with the subscription service listen address $2"},
	{regexp.MustCompile(`^名称 (.+) 已被目标本地入站占用$`), "Name $1 is already taken by a local inbound on the target"},
	{regexp.MustCompile(`^名称 (.+) 已被 Basics 自动策略使用，请修改 Outbound 名称$`), "Name $1 is used by an automatic Basics policy — rename the outbound"},
	{regexp.MustCompile(`^客户端 (.+) 缺少 UUID$`), "Client $1 has no UUID"},
	{regexp.MustCompile(`^客户端 (.+) 缺少密码$`), "Client $1 has no password"},
	{regexp.MustCompile(`^客户端名称 (.+) 重复，同一入口内必须唯一$`), "Duplicate client name $1 — names must be unique within an inbound"},
	{regexp.MustCompile(`^客户端 UUID (.+) 重复，同一入口内必须唯一$`), "Duplicate client UUID $1 — UUIDs must be unique within an inbound"},
	{regexp.MustCompile(`^Mihomo (\S+) listener 仅支持一个 密码 客户端$`), "Mihomo's $1 listener supports only one password client"},
	{regexp.MustCompile(`^Mihomo (\S+) listener 仅支持一个 (\S+) 客户端$`), "Mihomo's $1 listener supports only one $2 client"},
	{regexp.MustCompile(`^(\S+) 必须保留一个 密码 客户端，请停用或修改它$`), "$1 must keep one password client — disable or edit it instead"},
	{regexp.MustCompile(`^(\S+) 必须保留一个 (\S+) 客户端，请停用或修改它$`), "$1 must keep one $2 client — disable or edit it instead"},
	{regexp.MustCompile(`^(\S+) 入口必须配置证书和私钥$`), "A $1 inbound requires a certificate and a private key"},
	{regexp.MustCompile(`^(\S+) 入口不使用 TLS，也不需要证书$`), "A $1 inbound does not use TLS and needs no certificate"},
	{regexp.MustCompile(`^(\S+) 入口不支持 Reality / ShadowTLS / RestTLS / JLS 等传输层伪装$`), "A $1 inbound does not support transport camouflage such as Reality / ShadowTLS / RestTLS / JLS"},
	{regexp.MustCompile(`^(\S+) 入口没有 allow-insecure 选项$`), "A $1 inbound has no allow-insecure option"},
	{regexp.MustCompile(`^JLS 不支持 (\S+) 入口$`), "JLS does not support $1 inbounds"},
	{regexp.MustCompile(`^TLSMirror 不支持 (\S+) 入口$`), "TLSMirror does not support $1 inbounds"},
	{regexp.MustCompile(`^Snell v(\d+) 不支持 UDP，请改用 v3 及以上版本$`), "Snell v$1 has no UDP support — use v3 or newer"},
	{regexp.MustCompile(`^Reality Short ID (.+) 必须是长度不超过 16 的偶数位十六进制字符串$`), "Reality Short ID $1 must be an even-length hex string of at most 16 characters"},
	{regexp.MustCompile(`^Reality Short ID (.+) 不是有效的十六进制字符串$`), "Reality Short ID $1 is not a valid hex string"},
	{regexp.MustCompile(`^TLSMirror Primary Key 解码后必须正好 32 字节，当前 (\d+) 字节$`), "The TLSMirror Primary Key must decode to exactly 32 bytes, got $1"},
	{regexp.MustCompile(`^TLSMirror Explicit Nonce Cipher Suite (.+) 必须是 0-65535 的整数$`), "TLSMirror Explicit Nonce Cipher Suite $1 must be an integer between 0 and 65535"},
	{regexp.MustCompile(`^Hysteria2 启用 (\S+) 混淆时必须配置混淆密码$`), "Hysteria2 needs an obfuscation password when $1 obfuscation is enabled"},
	{regexp.MustCompile(`^Hysteria2 Masquerade 的 (\S+):// 必须带主机名$`), "Hysteria2 Masquerade with $1:// needs a host name"},
	{regexp.MustCompile(`^Hysteria2 Realm Fingerprint 必须是 32 字节的 SHA-256 指纹，当前 (\d+) 字节$`), "The Hysteria2 Realm fingerprint must be a 32-byte SHA-256 digest, got $1"},
	{regexp.MustCompile(`^已达到 (\d+) 个对等节点上限$`), "The limit of $1 peer nodes has been reached"},
	{regexp.MustCompile(`^请选择 1-(\d+) 个入站$`), "Select between 1 and $1 inbounds"},
	{regexp.MustCompile(`^每次最多同步 (\d+) 个客户端，请分批操作$`), "At most $1 clients can be synced at once — split the job"},
	{regexp.MustCompile(`^目标入站合并后超过 (\d+) 个 Client$`), "Merging would leave the target inbound with more than $1 clients"},
	{regexp.MustCompile(`^(\S+) 仅支持单 Client，目标已存在另一 Client$`), "$1 supports a single client and the target already has one"},
	{regexp.MustCompile(`^目标缺少路由出站 (.+)，请先在目标配置$`), "The target has no routing outbound $1 — configure it there first"},
	{regexp.MustCompile(`^入口 (.+) 未选择客户端$`), "No client selected for inbound $1"},
	{regexp.MustCompile(`^入口 (.+) 的客户端选择无效$`), "The client selection for inbound $1 is invalid"},
	{regexp.MustCompile(`^目标拒绝同步 \(HTTP (\d+)\)，请检查联机状态和版本$`), "The target refused the sync (HTTP $1) — check the link status and version"},
	{regexp.MustCompile(`^Cloudflare WARP 请求失败 \(HTTP (\d+)\)$`), "The Cloudflare WARP request failed (HTTP $1)"},
	{regexp.MustCompile(`^联机失败 \(HTTP (\d+)\)，请检查 token、对等信任和目标面板端口$`), "Link failed (HTTP $1) — check the token, peer trust and the target panel port"},
	{regexp.MustCompile(`^Outbound #(\d+) 名称不能为空、包含逗号/换行或超过 128 个字符$`), "Outbound #$1 needs a name without commas or line breaks, at most 128 characters"},
	{regexp.MustCompile(`^Outbound 名称 (.+) 与第 (\d+) 项重复$`), "Outbound name $1 duplicates item #$2"},
	{regexp.MustCompile(`^Outbound 名称 (.+) 是 Mihomo 内置策略名$`), "Outbound name $1 is a Mihomo built-in policy name"},
	{regexp.MustCompile(`^Outbound (.+) 的 kind 必须是 proxy 或 group$`), "The kind of outbound $1 must be proxy or group"},
	{regexp.MustCompile(`^Outbound (.+) 的 Dialer Proxy (.+) 不存在$`), "Dialer Proxy $2 of outbound $1 does not exist"},
	{regexp.MustCompile(`^Outbound (.+) 不能通过自身拨号$`), "Outbound $1 cannot dial through itself"},
	{regexp.MustCompile(`^策略组 (.+) 引用了不存在的 Outbound (.+)$`), "Proxy group $1 references the missing outbound $2"},
	{regexp.MustCompile(`^策略组 (.+) 的 Default Selected 必须在成员列表中$`), "The Default Selected of proxy group $1 must be one of its members"},
	{regexp.MustCompile(`^策略组存在循环引用，涉及 (.+)$`), "The proxy groups contain a reference cycle involving $1"},
	{regexp.MustCompile(`^不支持代理类型 (.+)$`), "Unsupported proxy type $1"},
	{regexp.MustCompile(`^不支持策略组类型 (.+)$`), "Unsupported proxy group type $1"},
	{regexp.MustCompile(`^不支持的类型 (.+)$`), "Unsupported type $1"},
	{regexp.MustCompile(`^OpenVPN Data Cipher (.+) 无效$`), "Invalid OpenVPN data cipher $1"},
	{regexp.MustCompile(`^Routing Rule #(\d+) 类型 (.+) 不受支持$`), "Routing Rule #$1 has the unsupported type $2"},
	{regexp.MustCompile(`^Routing Rule #(\d+) 引用了不存在的 Outbound (.+)$`), "Routing Rule #$1 references the missing outbound $2"},
	{regexp.MustCompile(`^无效匹配项 (.+)$`), "Invalid entry $1"},
	{regexp.MustCompile(`^(.+) 不是 IP、CIDR、private 或 geoip:国家代码$`), "$1 is not an IP, a CIDR, private or geoip:<country code>"},
	{regexp.MustCompile(`^(.+) 不是域名或 geosite/domain/full/keyword 匹配项$`), "$1 is not a domain or a geosite/domain/full/keyword entry"},
	{regexp.MustCompile(`^最多支持 (\d+) 项$`), "At most $1 entries are supported"},
	{regexp.MustCompile(`^第 (\d+) 项必须是节点对象$`), "Item #$1 must be a proxy object"},
	{regexp.MustCompile(`^第 (\d+) 项$`), "Item #$1"},
	{regexp.MustCompile(`^(\S+) 的类型 (\S+) 与所在数组不符$`), "The type $2 of $1 does not match the array it sits in"},
	{regexp.MustCompile(`^此编辑器仅接受 Outbound，不能导入顶层字段 (.+)$`), "This editor only accepts outbounds and cannot import the top-level field $1"},
	{regexp.MustCompile(`^(\S+) 必须是非空字符串$`), "$1 must be a non-empty string"},
	{regexp.MustCompile(`^(\S+) 必须是字符串，纯数字值请加引号$`), "$1 must be a string — quote purely numeric values"},
	{regexp.MustCompile(`^(\S+) 必须是 true 或 false$`), "$1 must be true or false"},
	{regexp.MustCompile(`^(\S+) 必须是非负整数$`), "$1 must be a non-negative integer"},
	{regexp.MustCompile(`^(\S+) 需要 Password$`), "$1 needs a Password"},
	{regexp.MustCompile(`^(\S+) 需要 Payload$`), "$1 needs a Payload"},
	{regexp.MustCompile(`^KCP Tunnel (\S+) 不能为负数$`), "KCP Tunnel $1 cannot be negative"},
	{regexp.MustCompile(`^KCP Tunnel MTU 必须在 (\d+)-(\d+) 之间$`), "The KCP Tunnel MTU must be between $1 and $2"},
	{regexp.MustCompile(`^KCP Tunnel Frame Size 不能超过 (\d+)（smux 帧长度是 16 位）$`), "The KCP Tunnel frame size cannot exceed $1 (smux frame length is 16-bit)"},
	{regexp.MustCompile(`^KCP Tunnel Data Shard 与 Parity Shard 之和不能超过 (\d+)$`), "KCP Tunnel Data Shard plus Parity Shard cannot exceed $1"},
	{regexp.MustCompile(`^Sudoku (\S+) 必须在 0-100 之间$`), "Sudoku $1 must be between 0 and 100"},
	{regexp.MustCompile(`^(\S+) Congestion Controller 不受 Mihomo 支持$`), "Mihomo does not support this $1 congestion controller"},
	{regexp.MustCompile(`^(\S+) BBR Profile 无效$`), "Invalid $1 BBR Profile"},
	{regexp.MustCompile(`^(\S+) BBR Profile 仅 bbr 与 bbr_meta_v2 拥塞控制支持$`), "The $1 BBR Profile only applies to the bbr and bbr_meta_v2 congestion controllers"},
	{regexp.MustCompile(`^(\S+) Init CWND 不能为负数$`), "$1 Init CWND cannot be negative"},
	{regexp.MustCompile(`^(\S+) Init CWND 仅 bbr 系列拥塞控制支持$`), "$1 Init CWND only applies to the bbr family of congestion controllers"},
	{regexp.MustCompile(`^无法连接 (\S+)：(.+)$`), "Cannot reach $1: $2"},
	{regexp.MustCompile(`^无法连接 (\S+)$`), "Cannot reach $1"},
	// 兜底规则：这些前缀后面跟的是入口/策略组的名字，名字本身不会带 ": "，所以
	// 捕获组特意排除它，免得把 fmt.Errorf("入口 %s: %w") 的下一层也一口吞掉。
	{regexp.MustCompile(`^入口 ((?:[^:]|:[^ ])+)$`), "Inbound $1"},
	{regexp.MustCompile(`^入站 ((?:[^:]|:[^ ])+)$`), "Inbound $1"},
	{regexp.MustCompile(`^策略组 ((?:[^:]|:[^ ])+)$`), "Proxy group $1"},
	{regexp.MustCompile(`^mTLS (\S+) 必须配置客户端 CA 证书$`), "mTLS $1 requires a client CA certificate"},
	{regexp.MustCompile(`^SNI 只能填写域名，不能包含空格或 / @ : \? # & 等字符: (.+)$`), "The SNI must be a bare domain name, with no spaces and none of / @ : ? # &: $1"},
}

// messagesEN 是整句对照表。键必须和代码里的中文字面量逐字节一致，值是英文界面里
// 显示的文案；带参数的提示只收录 ": " 之前那一段，参数留给 messagePatternsEN。
var messagesEN = map[string]string{
	// —— 面板、账号与设置 ——
	"请求无效":                    "Invalid request",
	"账号或密码错误":                 "Wrong username or password",
	"新账号和新密码不能为空":             "The new username and password cannot be empty",
	"当前账号或密码不正确":              "The current username or password is incorrect",
	"账号信息已更新，请重新登录":           "Credentials updated — please log in again",
	"密码不能为空":                  "The password cannot be empty",
	"面板正在重启":                  "The panel is restarting",
	"设置已保存":                   "Settings saved",
	"配置文件已生成":                 "Config file generated",
	"入口已保存":                   "Inbound saved",
	"入口已删除":                   "Inbound deleted",
	"Mihomo 配置测试通过":           "The Mihomo config test passed",
	"连接已关闭":                   "Connection closed",
	"核心状态已更新":                 "Core status updated",
	"流量统计已重置":                 "Traffic stats reset",
	"面板 URI 路径不能包含 . 或 .. 片段": "The panel URI path cannot contain . or .. segments",
	"面板 URI 路径只能包含字母、数字和 - _ . ~ 与 /": "The panel URI path may only contain letters, digits and - _ . ~ /",
	"订阅路径无效":     "Invalid subscription path",
	"Clash 路径无效": "Invalid Clash path",
	"订阅路径只能包含字母、数字和 - _ . ~":          "The subscription path may only contain letters, digits and - _ . ~",
	"Clash 路径只能包含字母、数字和 - _ . ~":      "The Clash path may only contain letters, digits and - _ . ~",
	"订阅路径和 Clash 路径不能相同":              "The subscription path and the Clash path cannot be the same",
	"代理端口必须在 1-65535 之间":              "The proxy port must be between 1 and 65535",
	"运行模式无效":                          "Invalid run mode",
	"面板 TLS 证书和私钥必须同时配置":              "The panel TLS certificate and private key must be set together",
	"面板 TLS 证书无效":                     "Invalid panel TLS certificate",
	"到期和流量提醒阈值不能为负数":                  "The expiry and traffic warning thresholds cannot be negative",
	"流量上报地址必须以 http:// 或 https:// 开头": "The traffic report URL must start with http:// or https://",
	"时区无效":    "Invalid time zone",
	"界面语言无效":  "Invalid interface language",
	"备注分隔符无效": "Invalid remark separator",
	"备注模板只支持 Inbound 和 Email 两个标签":   "The remark template only supports the Inbound and Email tags",
	"面板域名只能填主机名，不要带协议、端口或路径":         "The panel domain must be a bare host name — no scheme, port or path",
	"面板域名格式无效":                       "Invalid panel domain format",
	"订阅服务端口必须留空或在 1-65535 之间":        "The subscription service port must be empty or between 1 and 65535",
	"面板监听地址必须写成 IP:端口":               "The panel listen address must be written as IP:port",
	"面板监听地址的端口必须在 1-65535 之间":        "The panel listen address port must be between 1 and 65535",
	"面板监听地址只能填 IP，留空表示监听全部网卡":        "The panel listen address accepts an IP only — leave it empty to listen on every interface",
	"Mihomo API 地址必须写成 IP:端口":        "The Mihomo API address must be written as IP:port",
	"Mihomo API 地址的端口必须在 1-65535 之间": "The Mihomo API address port must be between 1 and 65535",
	"Mihomo API 地址只能填 IP，留空表示监听全部网卡": "The Mihomo API address accepts an IP only — leave it empty to listen on every interface",
	"订阅服务监听地址必须写成 IP:端口":             "The subscription service listen address must be written as IP:port",
	"订阅服务监听地址的端口必须在 1-65535 之间":      "The subscription service listen address port must be between 1 and 65535",
	"订阅服务监听地址只能填 IP，留空表示监听全部网卡":      "The subscription service listen address accepts an IP only — leave it empty to listen on every interface",

	// —— 入口与客户端 ——
	"Inbound 不存在":      "Inbound not found",
	"入口不存在":            "Inbound not found",
	"缺少入口 ID":          "Missing inbound ID",
	"入口名称不能为空":         "The inbound name cannot be empty",
	"入口类型不能为空":         "The inbound type cannot be empty",
	"端口必须在 1-65535 之间": "The port must be between 1 and 65535",
	"客户端数据无效":          "Invalid client data",
	"客户端已保存":           "Client saved",
	"客户端已删除":           "Client deleted",
	"客户端不存在":           "Client not found",
	"客户端名称不能为空":        "The client name cannot be empty",
	"此协议的客户端必须填写 UUID": "Clients of this protocol need a UUID",
	"用户名和密码不能为空":       "The username and password cannot be empty",
	"已启用密码认证时，至少需要一条用户名和密码": "Password authentication needs at least one username and password",
	"证书模式无效":                   "Invalid certificate mode",
	"TLS 入口需要同时配置证书和私钥":        "A TLS inbound needs both a certificate and a private key",
	"mTLS Client Auth Type 无效": "Invalid mTLS Client Auth Type",
	"ECH 需要同时配置证书和私钥":          "ECH needs both a certificate and a private key",
	"ECH 需要填写 SNI":             "ECH needs an SNI",
	"AnyTLS 必须配置证书、ShadowTLS、RestTLS，或明确启用 Allow Insecure":                       "AnyTLS needs a certificate, ShadowTLS or RestTLS — or Allow Insecure turned on explicitly",
	"Trojan 必须配置证书、Reality、ShadowTLS、RestTLS、JLS、Trojan SS，或明确启用 Allow Insecure": "Trojan needs a certificate, Reality, ShadowTLS, RestTLS, JLS or Trojan SS — or Allow Insecure turned on explicitly",
	"Shadowsocks 加密方式不受 Mihomo 支持":                                               "Mihomo does not support this Shadowsocks cipher",
	"Shadowsocks 仅支持一个客户端":                                                       "Shadowsocks supports only one client",
	"Shadowsocks 至少需要一个启用且有密码的客户端":                                               "Shadowsocks needs at least one enabled client with a password",
	"Snell 仅支持一个客户端":                                                             "Snell supports only one client",
	"Snell 至少需要一个启用且填写了 PSK 的客户端":                                                "Snell needs at least one enabled client with a PSK",
	"Snell 版本必须在 1-5 之间":                                                         "The Snell version must be between 1 and 5",
	"Snell Obfs Mode 不受 Mihomo 支持":                                               "Mihomo does not support this Snell Obfs Mode",
	"Snell 的 ShadowTLS / RestTLS / JLS 互斥，只能启用一种":                                "Snell's ShadowTLS / RestTLS / JLS are mutually exclusive — enable only one",
	"Snell 的 Obfs 与 ShadowTLS / RestTLS / JLS 互斥，只能启用一种":                         "Snell's Obfs and ShadowTLS / RestTLS / JLS are mutually exclusive — enable only one",
	"JLS 必须配置有效的目标地址，例如 example.com:443":                                         "JLS needs a valid destination, for example example.com:443",
	"JLS 必须配置用户名和密码":                                                             "JLS needs a username and a password",
	"Trojan SS 当前仅支持 Trojan 入口":                                                  "Trojan SS currently only works on Trojan inbounds",
	"Trojan SS 加密方式无效":                                                           "Invalid Trojan SS method",
	"Trojan SS 必须配置密码":                                                           "Trojan SS needs a password",
	"Reality 必须配置目标地址和私钥":                                                        "Reality needs a destination and a private key",
	"XHTTP Mode 无效":                         "Invalid XHTTP Mode",
	"XHTTP Padding Placement 无效":            "Invalid XHTTP Padding Placement",
	"XHTTP Padding Method 无效":               "Invalid XHTTP Padding Method",
	"XHTTP Uplink HTTP Method 无效":           "Invalid XHTTP Uplink HTTP Method",
	"XHTTP Session Placement 无效":            "Invalid XHTTP Session Placement",
	"XHTTP Sequence Placement 无效":           "Invalid XHTTP Sequence Placement",
	"XHTTP Uplink Data Placement 无效":        "Invalid XHTTP Uplink Data Placement",
	"VMess 的 mKCP 与 Mekya 不能同时启用":           "VMess cannot run mKCP and Mekya at the same time",
	"VMess Mekya 不能与 WebSocket 或 gRPC 同时启用": "VMess Mekya cannot run together with WebSocket or gRPC",
	"VMess mKCP 不能与 WebSocket 或 gRPC 同时启用":  "VMess mKCP cannot run together with WebSocket or gRPC",
	"VMess mKCP Header 无效":                  "Invalid VMess mKCP Header",
	"VMess Mekya KCP Header 无效":             "Invalid VMess Mekya KCP Header",
	"VMess Mekya 必须配置 URL":                  "VMess Mekya needs a URL",
	"VMess Mekya URL 必须是完整的 HTTPS URL":      "The VMess Mekya URL must be a complete HTTPS URL",

	// —— 协议专项校验 ——
	"TLSMirror 借用真实 TLS 服务器的握手，不能同时配置证书和私钥":                                "TLSMirror borrows a real TLS server's handshake — it cannot also carry a certificate and private key",
	"TLSMirror 与 Reality / ShadowTLS / RestTLS / JLS 互斥，只能启用一种伪装":          "TLSMirror and Reality / ShadowTLS / RestTLS / JLS are mutually exclusive — enable only one camouflage",
	"TLSMirror 必须配置 Primary Key":                                           "TLSMirror needs a Primary Key",
	"TLSMirror Primary Key 必须是标准 base64":                                   "The TLSMirror Primary Key must be standard base64",
	"TLSMirror 必须配置载体地址，例如 www.example.com:443":                            "TLSMirror needs a carrier address, for example www.example.com:443",
	"TLSMirror 载体地址必须是 host:port，例如 www.example.com:443":                   "The TLSMirror carrier address must be host:port, for example www.example.com:443",
	"TLSMirror 载体地址端口无效":                                                   "Invalid port in the TLSMirror carrier address",
	"Hysteria2 Obfs 不受 Mihomo 支持":                                          "Mihomo does not support this Hysteria2 Obfs",
	"Hysteria2 混淆报文长度不能为负数":                                                "The Hysteria2 obfuscation packet length cannot be negative",
	"Hysteria2 混淆报文长度仅 gecko 混淆支持":                                         "The Hysteria2 obfuscation packet length only applies to gecko obfuscation",
	"Hysteria2 混淆最小报文长度不能大于最大报文长度":                                         "The Hysteria2 minimum obfuscation packet length cannot exceed the maximum",
	"Hysteria2 Masquerade 不是有效的 URL":                                       "The Hysteria2 Masquerade is not a valid URL",
	"Hysteria2 Masquerade 的 file:// 需要三个斜杠的绝对目录，例如 file:///var/www":        "Hysteria2 Masquerade with file:// needs three slashes and an absolute directory, for example file:///var/www",
	"Hysteria2 Masquerade 的 file:// 必须带绝对目录，例如 file:///var/www":            "Hysteria2 Masquerade with file:// needs an absolute directory, for example file:///var/www",
	"Hysteria2 Masquerade 仅支持 file://、http:// 和 https://":                  "Hysteria2 Masquerade only supports file://, http:// and https://",
	"Hysteria2 BBR Profile 无效":                                             "Invalid Hysteria2 BBR Profile",
	"Hysteria2 Init CWND 不能为负数":                                            "The Hysteria2 Init CWND cannot be negative",
	"Hysteria2 UDP MTU 不能为负数":                                              "The Hysteria2 UDP MTU cannot be negative",
	"Hysteria2 Init Stream Window 不能大于 Max Stream Window":                  "The Hysteria2 Init Stream Window cannot exceed the Max Stream Window",
	"Hysteria2 Init Conn Window 不能大于 Max Conn Window":                      "The Hysteria2 Init Conn Window cannot exceed the Max Conn Window",
	"Hysteria2 Realm 必须配置 Server URL，例如 https://realm.hy2.io":              "Hysteria2 Realm needs a Server URL, for example https://realm.hy2.io",
	"Hysteria2 Realm Server URL 必须是完整的 http:// 或 https:// 地址":              "The Hysteria2 Realm Server URL must be a complete http:// or https:// address",
	"Hysteria2 Realm 必须配置 Token":                                           "Hysteria2 Realm needs a Token",
	"Hysteria2 Realm 必须配置 Realm ID":                                        "Hysteria2 Realm needs a Realm ID",
	"Hysteria2 Realm STUN 服务器必须是 host:port，例如 stun.sip.us:3478":            "A Hysteria2 Realm STUN server must be host:port, for example stun.sip.us:3478",
	"Hysteria2 Realm STUN 服务器端口无效":                                         "Invalid port on the Hysteria2 Realm STUN server",
	"Hysteria2 Realm Fingerprint 是证书 SHA-256 指纹，不能填浏览器指纹名":                 "The Hysteria2 Realm fingerprint is a certificate SHA-256 digest, not a browser fingerprint name",
	"Hysteria2 Realm Fingerprint 不是有效的十六进制字符串":                             "The Hysteria2 Realm fingerprint is not a valid hex string",
	"Hysteria2 Realm 的客户端证书和私钥必须同时配置":                                      "The Hysteria2 Realm client certificate and private key must be set together",
	"Hysteria2 Realm SNI 只能填写域名":                                           "The Hysteria2 Realm SNI must be a bare domain name",
	"Hysteria2 Realm Name Cert Verify 只能填写域名":                              "The Hysteria2 Realm Name Cert Verify must be a bare domain name",
	"Hysteria2 Realm 的 Realm 数量上限不能为负数":                                    "The Hysteria2 Realm limit cannot be negative",
	"Hysteria2 Realm Name Pattern 不是有效的正则":                                 "The Hysteria2 Realm Name Pattern is not a valid regular expression",
	"Hysteria2 Realm Trusted Proxy Header 只能是 HTTP 头名称，例如 X-Forwarded-For": "The Hysteria2 Realm Trusted Proxy Header must be an HTTP header name, for example X-Forwarded-For",
	"Hysteria2 Realm 是集合点 API，不需要客户端":                                      "Hysteria2 Realm is a rendezvous API and needs no clients",
	"Hysteria2 Realm 不支持 Reality / ShadowTLS / RestTLS / JLS 等传输层伪装":       "Hysteria2 Realm does not support transport camouflage such as Reality / ShadowTLS / RestTLS / JLS",
	"Hysteria2 Realm 没有 mux-option 选项":                                     "Hysteria2 Realm has no mux-option",
	"Hysteria2 Realm 没有 allow-insecure 选项":                                 "Hysteria2 Realm has no allow-insecure option",
	"TUIC Max Idle Time 不能为负数":                                             "The TUIC Max Idle Time cannot be negative",
	"TUIC Authentication Timeout 不能为负数":                                    "The TUIC Authentication Timeout cannot be negative",
	"TUIC Max UDP Relay Packet Size 不能为负数":                                 "The TUIC Max UDP Relay Packet Size cannot be negative",
	"AnyTLS Padding Scheme 每行都要写成 key=value，例如 stop=8":                     "Every AnyTLS Padding Scheme line must be key=value, for example stop=8",
	"AnyTLS Padding Scheme 必须包含 stop=N 指明第几个包之后停止填充（不能有多余空格）":              "The AnyTLS Padding Scheme must contain stop=N to say after which packet padding stops (no stray spaces)",
	"AnyTLS Padding Scheme 的 stop 必须是整数":                                   "The stop value of the AnyTLS Padding Scheme must be an integer",
	"KCP Tunnel 只有 Shadowsocks 入口支持":                                       "KCP Tunnel is only supported on Shadowsocks inbounds",
	"KCP Tunnel 需要填写 Key，否则内核会回落到公开的默认口令":                                  "KCP Tunnel needs a Key, otherwise the core falls back to the public default passphrase",
	"KCP Tunnel Crypt 不受 Mihomo 支持":                                        "Mihomo does not support this KCP Tunnel Crypt",
	"KCP Tunnel Mode 不受 Mihomo 支持":                                         "Mihomo does not support this KCP Tunnel Mode",
	"KCP Tunnel 与 simple-obfs 不能同时启用：客户端的 kcptun 插件不会再叠加 obfs":             "KCP Tunnel and simple-obfs cannot both be enabled: the client's kcptun plugin will not stack obfs on top",
	"KCP Tunnel 启用后监听端口只有 UDP，Shadow-TLS / RestTLS / JLS 都不会生效":            "With KCP Tunnel enabled the listener is UDP only, so Shadow-TLS / RestTLS / JLS have no effect",
	"KCP Tunnel smux 版本只支持 1 或 2":                                          "The KCP Tunnel smux version must be 1 or 2",
	"KCP Tunnel Stream Buffer 不能大于 smux Buffer":                            "The KCP Tunnel stream buffer cannot exceed the smux buffer",
	"Mieru 必须选择 Transport（TCP 或 UDP）":                                      "Mieru needs a Transport (TCP or UDP)",
	"Mieru Transport 只支持 TCP 或 UDP":                                        "The Mieru Transport must be TCP or UDP",
	"Mieru 至少需要一个启用的客户端":                                                   "Mieru needs at least one enabled client",
	"Mieru 客户端必须填写用户名":                                                     "Mieru clients need a username",
	"Mieru Traffic Pattern 必须是 mieru 工具导出的 base64 字符串":                     "The Mieru Traffic Pattern must be the base64 string exported by the mieru tool",
	"Sudoku 仅支持一个客户端":                                                      "Sudoku supports only one client",
	"Sudoku 至少需要一个启用且填写了 Key 的客户端":                                         "Sudoku needs at least one enabled client with a Key",
	"Sudoku AEAD Method 不受支持":                                              "Unsupported Sudoku AEAD Method",
	"Sudoku Table Type 不受支持":                                               "Unsupported Sudoku Table Type",
	"Sudoku Padding Max 不能小于 Padding Min":                                  "Sudoku Padding Max cannot be smaller than Padding Min",
	"Sudoku Handshake Timeout 不能为负数":                                       "The Sudoku Handshake Timeout cannot be negative",
	"Sudoku HTTP Mask Mode 不受支持":                                           "Unsupported Sudoku HTTP Mask Mode",
	"Sudoku Fallback 必须写成 host:port，例如 127.0.0.1:8080":                     "The Sudoku Fallback must be host:port, for example 127.0.0.1:8080",
	"Sudoku Fallback 的端口必须是数字":                                             "The Sudoku Fallback port must be numeric",
	"Sudoku Path Root 不能只有斜杠":                                              "The Sudoku Path Root cannot be just a slash",
	"Sudoku Path Root 只能是一段路径，不能包含 /":                                      "The Sudoku Path Root must be a single segment without /",
	"Sudoku Path Root 只能包含字母、数字、下划线和连字符":                                   "The Sudoku Path Root may only contain letters, digits, underscores and hyphens",
	"Sudoku Custom Table 只能由 x / p / v 组成":                                 "The Sudoku Custom Table may only contain x / p / v",
	"Sudoku Custom Table 必须是 8 个符号":                                        "The Sudoku Custom Table must have 8 symbols",
	"Sudoku Custom Table 必须包含 2 个 x、2 个 p、4 个 v":                           "The Sudoku Custom Table must contain 2 x, 2 p and 4 v",
	"ShadowQuic 必须配置 JLS Upstream 地址，例如 example.com:443":                   "ShadowQuic needs a JLS Upstream address, for example example.com:443",
	"ShadowQuic JLS Upstream 必须是 host:port，例如 example.com:443":             "The ShadowQuic JLS Upstream must be host:port, for example example.com:443",
	"ShadowQuic JLS Upstream 端口无效":                                         "Invalid port in the ShadowQuic JLS Upstream",
	"ShadowQuic QUIC 版本不受支持":                                               "Unsupported ShadowQuic QUIC version",
	"ShadowQuic 的 QUIC 参数不能为负数":                                            "The ShadowQuic QUIC parameters cannot be negative",
	"ShadowQuic 忽略客户端带宽时不能再设置 Down，否则协商会失败":                                "ShadowQuic cannot set Down while ignoring the client bandwidth — negotiation would fail",
	"ShadowQuic 至少需要一个启用的客户端":                                              "ShadowQuic needs at least one enabled client",
	"ShadowQuic 客户端必须有用户名":                                                 "ShadowQuic clients need a username",
	"ShadowQuic 由 JLS 认证握手，不需要证书":                                          "ShadowQuic authenticates through JLS and needs no certificate",
	"ShadowQuic 不支持 Reality / ShadowTLS / RestTLS / JLS 等传输层伪装":            "ShadowQuic does not support transport camouflage such as Reality / ShadowTLS / RestTLS / JLS",
	"ShadowQuic 没有 allow-insecure 选项":                                      "ShadowQuic has no allow-insecure option",
	"TrustTunnel 入口必须配置证书和私钥":                                              "A TrustTunnel inbound needs a certificate and a private key",
	"TrustTunnel 必须选择监听的网络类型":                                              "TrustTunnel needs at least one listening network type",
	"TrustTunnel Network 只支持 tcp 和 udp":                                    "The TrustTunnel Network only supports tcp and udp",
	"TrustTunnel Network 至少要包含 tcp 或 udp":                                  "The TrustTunnel Network must include tcp or udp",
	"TrustTunnel 的拥塞控制仅在监听 udp（QUIC）时生效":                                   "TrustTunnel congestion control only applies when listening on udp (QUIC)",
	"TrustTunnel 至少需要一个启用的客户端":                                             "TrustTunnel needs at least one enabled client",
	"TrustTunnel 客户端必须有用户名":                                                "TrustTunnel clients need a username",
	"TrustTunnel 不支持 Reality / ShadowTLS / RestTLS / JLS 等传输层伪装":           "TrustTunnel does not support transport camouflage such as Reality / ShadowTLS / RestTLS / JLS",
	"TrustTunnel 没有 mux-option 选项":                                         "TrustTunnel has no mux-option",
	"TrustTunnel 没有 allow-insecure 选项":                                     "TrustTunnel has no allow-insecure option",
	"请先设置 Mihomo 核心路径":                                                     "Set the Mihomo core path first",
	"找不到 Mihomo 核心":                                                        "Mihomo core not found",
	"Mihomo 配置测试失败":                                                        "The Mihomo config test failed",
	"Mihomo 自动启动失败":                                                        "Mihomo automatic startup failed",

	// —— Mihomo 分流、Outbound 与工具 ——
	"Mihomo 分流设置已保存":   "Mihomo routing settings saved",
	"Inbound YAML 已导入": "Inbound YAML imported",
	"YAML 中没有 Inbound": "The YAML contains no inbound",
	"YAML 顶层必须是一个 listener、单项数组或 listeners 数组":                        "The YAML top level must be one listener, a single-item array, or a listeners array",
	"每次只能导入一个 Inbound":                                                "Only one inbound can be imported at a time",
	"Inbound YAML 必须是 listener 对象":                                    "Inbound YAML must be a listener object",
	"Inbound YAML 包含不支持的数据":                                           "Inbound YAML contains unsupported data",
	"找不到可用的五位数 Inbound 端口":                                            "No available five-digit inbound port was found",
	"GLOBAL 只能定义为策略组":                                                 "GLOBAL can only be defined as a proxy group",
	"Server 必须是域名或 IP，且不能包含协议、路径或空格":                                  "Server must be a domain or an IP, without scheme, path or spaces",
	"Port 必须在 1-65535 之间":                                             "Port must be between 1 and 65535",
	"Routing Mark 不能为负数":                                              "Routing Mark cannot be negative",
	"Interface Name 不能包含换行，且不能超过 256 个字符":                             "Interface Name cannot contain line breaks or exceed 256 characters",
	"Alter ID 不能为负数":                                                  "Alter ID cannot be negative",
	"IP Version 无效":                                                   "Invalid IP Version",
	"Network 首版仅支持 tcp、ws 和 grpc":                                     "Network currently supports only tcp, ws and grpc",
	"gRPC Network 需要 Service Name":                                    "A gRPC network needs a Service Name",
	"Shadowsocks 需要 Cipher 和 Password":                                "Shadowsocks needs a Cipher and a Password",
	"UUID 格式无效":                                                       "Invalid UUID format",
	"TUIC 需要 Token，或同时填写 UUID 和 Password":                             "TUIC needs a Token, or both a UUID and a Password",
	"Direct 不使用 Server 或 Port":                                        "Direct does not use a Server or Port",
	"WireGuard 至少需要一个本地 IP 或 IPv6 地址":                                 "WireGuard needs at least one local IPv4 or IPv6 address",
	"WireGuard IP 必须是有效的 IPv4 地址或 CIDR":                               "WireGuard IP must be a valid IPv4 address or CIDR",
	"WireGuard IPv6 必须是有效的 IPv6 地址或 CIDR":                             "WireGuard IPv6 must be a valid IPv6 address or CIDR",
	"WireGuard Private Key 必须是 32 字节 Base64":                          "The WireGuard Private Key must be 32 bytes of base64",
	"WireGuard Public Key 必须是 32 字节 Base64":                           "The WireGuard Public Key must be 32 bytes of base64",
	"WireGuard Pre-shared Key 必须是 32 字节 Base64":                       "The WireGuard Pre-shared Key must be 32 bytes of base64",
	"WireGuard Reserved 必须留空或填写 3 个字节":                                "WireGuard Reserved must be empty or contain three bytes",
	"WireGuard Reserved 每项必须在 0-255 之间":                               "Each WireGuard Reserved byte must be between 0 and 255",
	"WireGuard MTU、Workers、Persistent Keepalive 和刷新间隔不能为负数":           "WireGuard MTU, Workers, Persistent Keepalive and refresh interval cannot be negative",
	"WireGuard Remote DNS Resolve 开启时必须填写 DNS":                        "WireGuard DNS is required when Remote DNS Resolve is enabled",
	"OpenVPN Proto 只能是 udp 或 tcp":                                     "OpenVPN Proto must be udp or tcp",
	"OpenVPN 当前只支持 Dev tun":                                           "OpenVPN currently supports only Dev tun",
	"OpenVPN Cipher 无效":                                               "Invalid OpenVPN Cipher",
	"OpenVPN Auth 无效":                                                 "Invalid OpenVPN Auth",
	"OpenVPN Comp LZO 只能是 yes、no 或 adaptive":                          "OpenVPN Comp LZO must be yes, no or adaptive",
	"OpenVPN CA 必须是有效的内联 PEM 内容":                                      "OpenVPN CA must contain valid inline PEM data",
	"OpenVPN Cert 和 Key 必须同时填写":                                       "OpenVPN Cert and Key must be set together",
	"OpenVPN Cert 和 Key 必须是有效的内联 PEM 内容":                              "OpenVPN Cert and Key must contain valid inline PEM data",
	"OpenVPN 需要 Cert + Key，或填写 Username 使用 auth-user-pass":            "OpenVPN needs Cert + Key, or a Username for auth-user-pass",
	"OpenVPN TLS Auth、TLS Crypt 和 TLS Crypt v2 只能选择一种":                "Only one of OpenVPN TLS Auth, TLS Crypt and TLS Crypt v2 may be used",
	"OpenVPN Key Direction 只能是 0 或 1":                                 "OpenVPN Key Direction must be 0 or 1",
	"OpenVPN Ping、Ping Restart、Handshake Timeout 和 MTU 不能为负数":         "OpenVPN Ping, Ping Restart, Handshake Timeout and MTU cannot be negative",
	"OpenVPN Remote DNS Resolve 开启时必须填写 DNS":                          "OpenVPN DNS is required when Remote DNS Resolve is enabled",
	"该协议不支持 Reality":                                                  "This protocol does not support Reality",
	"Reality 需要 Public Key":                                           "Reality needs a Public Key",
	"至少需要一个代理或策略组成员":                                                  "At least one proxy or proxy group member is required",
	"不能引用自身":                                                          "It cannot reference itself",
	"Test URL 必须是完整的 http:// 或 https:// 地址":                           "The Test URL must be a complete http:// or https:// address",
	"Interval、Timeout 和 Tolerance 不能为负数":                              "Interval, Timeout and Tolerance cannot be negative",
	"Load Balance Strategy 无效":                                        "Invalid Load Balance Strategy",
	"MATCH 规则不能包含 Payload 或 no-resolve":                               "A MATCH rule cannot carry a Payload or no-resolve",
	"MATCH 只能出现一次且必须是最后一条规则":                                          "MATCH may appear only once, and only as the last rule",
	"MATCH 后面不能再添加规则":                                                 "No rule may follow MATCH",
	"no-resolve 只适用于目标 IP 类规则":                                        "no-resolve only applies to destination-IP rules",
	"Payload 不能包含逗号":                                                  "The Payload cannot contain commas",
	"CIDR 格式无效":                                                       "Invalid CIDR format",
	"NETWORK 只能是 tcp 或 udp":                                           "NETWORK must be tcp or udp",
	"正则表达式无效":                                                         "Invalid regular expression",
	"Payload 必须是非负整数":                                                 "The Payload must be a non-negative integer",
	"端口必须是单个端口或 start-end":                                            "The port must be a single port or start-end",
	"端口范围起点不能大于终点":                                                    "The start of the port range cannot be greater than its end",
	"Routing Mode 无效":                                                 "Invalid Routing Mode",
	"Direct IP Version 无效":                                            "Invalid Direct IP Version",
	"Outbound Test URL 必须是完整的 HTTP / HTTPS 地址":                        "The Outbound Test URL must be a complete HTTP / HTTPS address",
	"Traffic Sampling Interval 必须在 1-10 秒之间":                          "The Traffic Sampling Interval must be between 1 and 10 seconds",
	"Traffic Save Interval 必须在 1-300 秒之间":                             "The Traffic Save Interval must be between 1 and 300 seconds",
	"Log Level 无效":                                                    "Invalid Log Level",
	"Log Buffer Size 必须在 100-10000 行之间":                               "The Log Buffer Size must be between 100 and 10000 lines",
	"GEOIP 需使用两位国家代码":                                                 "GEOIP needs a two-letter country code",
	"Geosite 分类无效":                                                    "Invalid Geosite category",
	"WARP Routing 需要有效的 WARP WireGuard Outbound，请先添加或清空 WARP Routing": "WARP Routing needs a valid WARP WireGuard outbound — add one or clear WARP Routing",
	"Outbound 不存在或不支持测试":                                              "The outbound does not exist or cannot be tested",
	"请先启动 Mihomo，并应用已保存的配置":                                           "Start Mihomo first and apply the saved config",
	"Outbound 延迟测试失败":                                                 "The outbound latency test failed",
	"Outbound 不可用，或尚未应用到运行中的 Mihomo":                                  "The outbound is unavailable, or has not been applied to the running Mihomo",
	"Mihomo 返回了无效的延迟结果":                                               "Mihomo returned an invalid latency result",
	"编辑 Outbound 时 YAML 必须只包含一个节点或策略组":                                "When editing an outbound the YAML must contain exactly one proxy or proxy group",
	"YAML 不能超过 512 KB":                                                "The YAML cannot exceed 512 KB",
	"YAML 解析失败":                                                       "Failed to parse the YAML",
	"一次只能导入一个 YAML 文档":                                                "Only one YAML document can be imported at a time",
	"proxies / proxy-groups 必须是 YAML 数组":                              "proxies / proxy-groups must be YAML arrays",
	"YAML 顶层必须是节点对象或数组":                                               "The top level of the YAML must be a proxy object or an array",
	"YAML 中没有 Outbound":                                               "The YAML contains no outbound",
	"YAML 包含非字符串键或不支持的数据":                                             "The YAML contains non-string keys or unsupported data",
	"策略组 proxies 必须是名称数组":                                             "The proxies of a proxy group must be an array of names",
	"策略组成员必须是非空名称":                                                    "Proxy group members must be non-empty names",
	"请填入一条有效的节点分享链接":                                                  "Paste a valid node share link",
	"无法解析节点分享链接，请检查协议和格式":                                             "Cannot parse the share link — check the protocol and format",
	"Reality 私钥不是有效的 Base64URL":                                       "The Reality private key is not valid Base64URL",
	"Reality 私钥解码后必须为 32 字节":                                          "The Reality private key must decode to 32 bytes",
	"Reality 私钥无效":                                                    "Invalid Reality private key",
	"请选择 X25519 或 ML-KEM-768 Authentication":                          "Choose X25519 or ML-KEM-768 authentication",
	"尚未配置 Mihomo 核心路径":                                                "The Mihomo core path is not configured yet",
	"未配置 Mihomo 核心路径":                                                 "The Mihomo core path is not configured",
	"Mihomo 核心不可用":                                                    "The Mihomo core is unavailable",
	"ECH 密钥生成失败":                                                      "ECH key generation failed",
	"Mihomo 未返回有效的 ECH 密钥":                                            "Mihomo returned no valid ECH key",

	// —— 备份与核心维护 ——
	"无法读取备份文件":                     "Cannot read the backup file",
	"请选择 m-ui 备份文件":                "Choose an m-ui backup file",
	"备份恢复成功，请按需重启核心":               "Backup restored — restart the core if needed",
	"版本标签无效":                       "Invalid version tag",
	"Geofile 更新完成":                 "Geofiles updated",
	"备份文件超过 32 MB 限制":              "The backup file exceeds the 32 MB limit",
	"备份文件不是有效的 ZIP":                "The backup file is not a valid ZIP",
	"备份中缺少 state.json":             "state.json is missing from the backup",
	"state.json 无效":                "Invalid state.json",
	"备份缺少必要的面板设置":                  "The backup is missing required panel settings",
	"备份中的默认端口无效":                   "The default port in the backup is invalid",
	"备份中的订阅服务端口无效":                 "The subscription service port in the backup is invalid",
	"备份中的 Mihomo Outbounds 无效":     "The Mihomo Outbounds in the backup are invalid",
	"备份中的 Mihomo Routing Rules 无效": "The Mihomo Routing Rules in the backup are invalid",
	"备份中的 Mihomo Basics 无效":        "The Mihomo Basics in the backup are invalid",
	"备份中的 WARP 账户无效":               "The WARP account in the backup is invalid",
	"下载文件 SHA-256 校验失败":            "SHA-256 verification of the downloaded file failed",
	"新核心无法运行":                      "The new core failed to run",
	"备份旧核心失败":                      "Failed to back up the old core",
	"替换核心失败":                       "Failed to replace the core",
	"核心已更新，但重新启动失败":                "The core was updated but failed to restart",
	"ZIP 中没有 Mihomo 可执行文件":         "The ZIP contains no Mihomo executable",
	"拒绝未授权的下载地址":                   "Refused an unauthorized download URL",
	"下载文件超过大小限制":                   "The downloaded file exceeds the size limit",
	"文件超过大小限制":                     "The file exceeds the size limit",

	// —— WARP ——
	"无法构造 WARP 请求": "Cannot build the WARP request",
	"无法连接 Cloudflare WARP，请检查服务器网络或稍后重试":                     "Cannot reach Cloudflare WARP — check the server network or try again later",
	"Cloudflare WARP 响应读取失败或过大":                              "The Cloudflare WARP response could not be read or was too large",
	"Cloudflare WARP 返回了无效的对象":                               "Cloudflare WARP returned an invalid object",
	"Cloudflare WARP 返回了无效的 JSON":                            "Cloudflare WARP returned invalid JSON",
	"Cloudflare WARP 拒绝了请求，请检查账户或 License Key":               "Cloudflare WARP refused the request — check the account or the License Key",
	"Cloudflare WARP 响应格式不符合预期":                              "The Cloudflare WARP response has an unexpected shape",
	"WARP 账户缺少有效的 Device ID / Access Token":                  "The WARP account has no valid Device ID / Access Token",
	"WARP Private Key 无效":                                    "Invalid WARP Private Key",
	"WireGuard 密钥必须是 32 字节 Base64":                           "A WireGuard key must be 32 bytes of base64",
	"WARP 账户保存失败":                                            "Failed to save the WARP account",
	"请先创建 WARP 账户":                                           "Create a WARP account first",
	"Cloudflare 返回的 Device ID 不一致":                           "Cloudflare returned a mismatched Device ID",
	"License Key 格式应为 XXXXXXXX-XXXXXXXX-XXXXXXXX":            "The License Key must look like XXXXXXXX-XXXXXXXX-XXXXXXXX",
	"Cloudflare 返回了无效的隧道 IP 地址":                              "Cloudflare returned an invalid tunnel IP address",
	"Cloudflare 尚未返回 WireGuard peer，请点击 More Information 重试": "Cloudflare has not returned a WireGuard peer yet — click More Information to retry",
	"Cloudflare 返回的 WireGuard Public Key 无效":                 "Cloudflare returned an invalid WireGuard Public Key",
	"Cloudflare 返回的 WireGuard Endpoint 无效":                   "Cloudflare returned an invalid WireGuard Endpoint",
	"Cloudflare 返回的 client_id 无效，应为 3 字节 Base64":             "Cloudflare returned an invalid client_id — it must be 3 bytes of base64",
	"Cloudflare 尚未返回隧道 IP 地址":                                "Cloudflare has not returned a tunnel IP address yet",

	// —— 联机与同步 ——
	"Multi-control 未初始化":              "Multi-control is not initialized",
	"Multi-control 未初始化，请检查面板日志":      "Multi-control is not initialized — check the panel log",
	"Multi-control 状态无效":              "Invalid multi-control state",
	"Multi-control 私钥无效":              "Invalid multi-control private key",
	"Multi-control 身份与私钥不符":           "The multi-control identity does not match the private key",
	"Multi-control 配对 token 无效":       "Invalid multi-control pairing token",
	"Multi-control 节点数量超过限制":          "Too many multi-control nodes",
	"Multi-control 断开记录无效":            "Invalid multi-control disconnect record",
	"Multi-control 节点同时处于连接和断开状态":     "A multi-control node is both connected and disconnected",
	"Multi-control 节点记录无效":            "Invalid multi-control node record",
	"联机地址必须是 http(s)://主机:面板端口，不含路径":  "The link address must be http(s)://host:panel-port, with no path",
	"联机地址缺少主机或有效端口":                   "The link address is missing a host or a valid port",
	"联机地址不能使用通配、组播或链路本地地址":            "The link address cannot be a wildcard, multicast or link-local address",
	"联机主机名无效":                         "Invalid link host name",
	"联机协议格式无效":                        "Invalid link protocol format",
	"联机消息时间无效，请校准服务器时间":               "Invalid link message timestamp — synchronise the server clock",
	"联机消息签名无效":                        "Invalid link message signature",
	"联机响应过大或无法读取":                     "The link response was too large or could not be read",
	"节点身份无效":                          "Invalid node identity",
	"节点身份签名无效":                        "Invalid node identity signature",
	"节点联机地址无效":                        "Invalid node link address",
	"节点名称需为 1-128 字符":                 "The node name must be 1-128 characters",
	"节点 ID 无效":                        "Invalid node ID",
	"节点不存在":                           "Node not found",
	"配对 token 必须为 16 至 32 位小写字母和数字混合": "The pairing token must be 16 to 32 lowercase letters and digits",
	"Token 必须是 16 至 32 位小写字母和数字混合":    "The token must be 16 to 32 lowercase letters and digits",
	"协议必须为 auto/http/https":           "The protocol must be auto/http/https",
	"请先设置本机联机地址":                      "Set this panel's own link address first",
	"对方未公布联机地址":                       "The peer has not published a link address",
	"对方尚未公布联机地址":                      "The peer has not published a link address yet",
	"不能连接本面板或具有相同身份的副本":               "Cannot connect to this panel, or to a copy sharing its identity",
	"该节点已在本面板断开":                      "This node has been disconnected on this panel",
	"目标返回了无效的联机响应":                    "The target returned an invalid link response",
	"目标未能证明持有配对 token":                "The target could not prove it holds the pairing token",
	"目标节点身份发生变化，请重新配对":                "The target node's identity changed — pair again",
	"连接已断开":                           "The connection was closed",
	"同步任务不存在或已过期，请检查目标面板后重新操作":        "The sync job does not exist or has expired — check the target panel and start again",
	"同步选项无效":                          "Invalid sync options",
	"任务 ID 无效":                        "Invalid job ID",
	"已有同步任务正在进行":                      "A sync job is already running",
	"请选择目标面板":                         "Choose a target panel",
	"目标面板已离线、断开或重复，请重新打开选择窗口":         "The target panel is offline, disconnected or duplicated — reopen the picker",
	"同步内容超过 4 MiB，请分批同步":              "The sync payload exceeds 4 MiB — split it into batches",
	"入站已变化或选择重复，请重新打开同步窗口":            "The inbounds changed or a selection is duplicated — reopen the sync window",
	"无法读取入站配置引用的证书文件":                 "Cannot read the certificate file referenced by the inbound",
	"证书文件无效或超过 256 KiB":               "The certificate file is invalid or larger than 256 KiB",
	"同步证书必须为 PEM 内容，不能引用目标本地路径":       "Synced certificates must be PEM content, not a path on the target",
	"入站证书与私钥无效或不匹配":                   "The inbound certificate and private key are invalid or do not match",
	"客户端 CA 证书内容无效":                   "The client CA certificate content is invalid",
	"保存目标配置失败":                        "Failed to save the target config",
	"保存失败且 YAML 回滚失败，请在目标面板重新生成配置":    "Saving failed and the YAML rollback failed — regenerate the config on the target panel",
	"入站或客户端信息无效":                      "Invalid inbound or client data",
	"入站 ID 与目标已有入站冲突":                 "The inbound ID collides with an existing inbound on the target",
	"入站监听地址必须是有效 IP":                  "The inbound listen address must be a valid IP",
	"客户端身份无效":                         "Invalid client identity",
	"同步来源的协议已变化，请在目标确认后移除旧副本":         "The source protocol changed — confirm on the target, then remove the old copy",
	"已保存，重启目标 Mihomo 后生效":             "Saved — restart the target Mihomo to apply",
	"同步协议格式无效":                        "Invalid sync protocol format",
	"请校准两台服务器时间":                      "Synchronise the clocks on both servers",
	"对等节点未受信任或已断开":                    "The peer is untrusted or disconnected",
	"同步消息签名无效":                        "Invalid sync message signature",
	"重复的同步消息":                         "Duplicate sync message",
	"同步请求过多":                          "Too many sync requests",
	"同步密文无效":                          "Invalid sync ciphertext",
	"目标面板已断开，同步已停止":                   "The target panel disconnected — the sync stopped",
	"连接中断或超时；结果可能未返回，请检查目标后重试（重复同步会合并）": "The connection dropped or timed out; the result may not have come back — check the target and retry (repeat syncs are merged)",
	"目标面板版本不支持 Sync Inbound，请先升级目标面板":   "The target panel's version does not support Sync Inbound — upgrade it first",
	"目标同步响应无效，请检查目标面板结果":                "The target returned an invalid sync response — check the result on the target panel",
	"目标会话密钥无效":            "Invalid target session key",
	"同步结果与任务不符":           "The sync result does not match the job",
	"同步结果解密失败":            "Failed to decrypt the sync result",
	"同步结果数量不符，请检查目标面板":    "The number of sync results does not match — check the target panel",
	"同步结果与入站不符":           "The sync result does not match the inbound",
	"拉取结果与任务不符":           "The pull result does not match the job",
	"拉取结果解密失败":            "Failed to decrypt the pull result",
	"目标返回的入站信息无效":         "The target returned invalid inbound data",
	"目标返回的 Client 数量超过限制": "The target returned more clients than allowed",

	// —— 跨面板订阅与客户端订阅 ——
	"跨面板订阅未初始化":                              "Cross-panel subscription is not initialized",
	"跨面板订阅缓存 token 无效":                       "Invalid cross-panel subscription cache token",
	"跨面板订阅缓存入站数量超过限制":                        "The cross-panel subscription cache holds too many inbounds",
	"跨面板订阅缓存节点身份无效":                          "Invalid node identity in the cross-panel subscription cache",
	"跨面板订阅缓存无效":                              "Invalid cross-panel subscription cache",
	"跨面板订阅缓存节点无效":                            "Invalid node in the cross-panel subscription cache",
	"跨面板订阅路径无效":                              "Invalid cross-panel subscription path",
	"跨面板订阅路径必须是 /isub 加 16 位小写字母和数字混合 token": "The cross-panel subscription path must be /isub plus a 16-character token of lowercase letters and digits",
	"跨面板订阅路径不能与 Subscription Path 相同":        "The cross-panel subscription path cannot be the same as the Subscription Path",
	"跨面板订阅路径不能与面板 URI 路径相同":                  "The cross-panel subscription path cannot be the same as the panel URI path",
	"不支持的跨面板订阅缓存版本":                          "Unsupported cross-panel subscription cache version",
	"已有跨面板节点拉取任务正在进行":                        "A cross-panel node pull is already running",
	"当前正在拉取，完成后再清除缓存":                        "A pull is in progress — clear the cache after it finishes",
	"拉取任务不存在":                                "The pull job does not exist",
	"暂无已配对面板":                                "No paired panels yet",
	"本机入站数量超过同步上限":                           "This panel has more inbounds than the sync limit allows",
	"清除跨面板订阅缓存失败":                            "Failed to clear the cross-panel subscription cache",
	"缓存保存失败":                                 "Failed to save the cache",
	"面板当前离线":                                 "The panel is currently offline",
	"未知的客户端订阅格式":                             "Unknown client subscription format",
}
