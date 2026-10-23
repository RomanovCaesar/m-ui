package app

import (
	"fmt"
	"testing"
)

// One line per addLogLocked / addLog call site, written the way the code
// writes it.
func samplePanelLogLines() []string {
	lines := []string{
		"Mihomo 自动启动失败: 找不到 Mihomo 核心: /usr/local/m-ui/bin/mihomo",
		"Mihomo 已启动",
		"Mihomo 已退出: exit status 1",
		"Mihomo 已停止",
		"面板数据已从备份恢复",
		"Mihomo 核心已切换: v1.19.33",
		"GeoIP.dat 已通过 jsDelivr 备用线路下载",
		fmt.Sprintf("下载 %s 线路 %d 失败: %v", "geoip.metadb", 1, "context deadline exceeded"),
		"GeoIP.dat、GeoSite.dat 与 geoip.metadb 已更新",
		fmt.Sprintf("Inbound %s 已到期或耗尽，正在停止它的监听", "hk-443"),
		fmt.Sprintf("客户端 %s/%s 已到期或耗尽，正在应用认证变更", "hk-443", "alice"),
		"应用客户端配额失败: exit status 1",
		"外部流量上报失败: HTTP 502",
		fmt.Sprintf("Inbound %s 已执行周期流量重置（%s）", "hk-443", "monthly"),
		fmt.Sprintf("客户端 %s/%s 已自动续期至 %s（第 %d 次）", "hk-443", "alice", "2026-11-01 00:00", 2),
		fmt.Sprintf("客户端 %s/%s 已执行周期流量重置（%s）", "hk-443", "alice", "daily"),
		"应用流量重置失败: exit status 1",
		"VPNGate 安装失败: exit status 2",
		"VPNGate 安装后写入 Mihomo 配置失败: exit status 2",
		"VPNGate: 开始下载 https://github.com/SoftEtherVPN/SoftEtherVPN_Stable/releases/download/v4.44/x.tar.gz",
		"VPNGate: 开始编译客户端",
		"VPNGate: 客户端安装完成 v4.44-9807-rtm",
		"VPNGate 服务停止失败: exit status 5",
		fmt.Sprintf("VPNGate 槽位 %d 禁用网卡 %s 失败，继续尝试删除: %v", 0, "vpn_vpn0", "exit status 1"),
		fmt.Sprintf("VPNGate 槽位 %d 清理失败: %v", 1, "exit status 1"),
		fmt.Sprintf("VPNGate 槽位 %d: %v", 2, "dial timeout"),
		fmt.Sprintf("VPNGate 槽位 %d 已连接 %s [%s]", 3, "203.0.113.7:443", "JP NTT"),
	}
	for i := range lines {
		lines[i] = "12:30:00  " + lines[i]
	}
	return lines
}

func TestPanelLogsAreTranslated(t *testing.T) {
	lines := append(samplePanelLogLines(), "12:30:01  [TCP] 10.0.0.2:5000 --> example.com:443 match DomainSuffix using DIRECT")
	if got := translateLogs(lines, defaultLanguage); got[1] != lines[1] {
		t.Fatalf("zh-CN must keep the original: %q", got[1])
	}
	for language := range messagesLocalized {
		translated := translateLogs(lines, language)
		for i, line := range translated {
			if language != "zh-TW" && language != "ja" && hasCJK(line) {
				t.Errorf("%s: untranslated log line %q", language, line)
			}
			if line[:10] != "12:30:00  " && line[:10] != "12:30:01  " {
				t.Errorf("%s: lost the timestamp: %q", language, line)
			}
			if i == len(lines)-1 && line != lines[i] {
				t.Errorf("%s: Mihomo's own output changed: %q", language, line)
			}
		}
	}
	english := translateLogs(lines, "en")
	for index, want := range map[int]string{
		14: "12:30:00  Client hk-443/alice renewed until 2026-11-01 00:00 (renewal 2)",
		13: "12:30:00  Inbound hk-443: scheduled traffic reset (monthly)",
		2:  "12:30:00  Mihomo exited: exit status 1",
		25: "12:30:00  VPNGate slot 2: dial timeout",
	} {
		if english[index] != want {
			t.Errorf("en line %d = %q, want %q", index, english[index], want)
		}
	}
}
