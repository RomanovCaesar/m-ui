package app

import (
	"net"
	"runtime"
	"strings"
	"sync"
	"time"
)

type SystemStats struct {
	CPUPercent   float64    `json:"cpuPercent"`
	CPUCores     int        `json:"cpuCores"`
	CPUSpeedMHz  float64    `json:"cpuSpeedMhz"`
	MemoryUsed   uint64     `json:"memoryUsed"`
	MemoryTotal  uint64     `json:"memoryTotal"`
	SwapUsed     uint64     `json:"swapUsed"`
	SwapTotal    uint64     `json:"swapTotal"`
	DiskUsed     uint64     `json:"diskUsed"`
	DiskTotal    uint64     `json:"diskTotal"`
	Load         [3]float64 `json:"load"`
	OSUptime     uint64     `json:"osUptime"`
	CoreUptime   uint64     `json:"coreUptime"`
	PanelUptime  uint64     `json:"panelUptime"`
	ProcessRAM   uint64     `json:"processRam"`
	ProcessTasks int        `json:"processTasks"`
	IPv4         []string   `json:"ipv4"`
	IPv6         []string   `json:"ipv6"`
	TCP          int        `json:"tcp"`
	UDP          int        `json:"udp"`
}

func collectSystemStats(dataDir string) SystemStats {
	stats := collectPlatformStats(dataDir)
	stats.CPUCores = runtime.NumCPU()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	stats.ProcessRAM = memory.Sys
	stats.ProcessTasks = runtime.NumGoroutine()
	stats.IPv4, stats.IPv6 = interfaceAddresses()
	return stats
}

func interfaceAddresses() ([]string, []string) {
	var ipv4, ipv6 []string
	interfaces, err := net.Interfaces()
	if err != nil {
		return ipv4, ipv6
	}
	seen := map[string]bool{}
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			host := address.String()
			if slash := strings.IndexByte(host, '/'); slash >= 0 {
				host = host[:slash]
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || seen[host] {
				continue
			}
			seen[host] = true
			if ip.To4() != nil {
				ipv4 = append(ipv4, host)
			} else {
				ipv6 = append(ipv6, host)
			}
		}
	}
	return ipv4, ipv6
}

func applyConnectionStats(stats *SystemStats, snapshot any) {
	root, ok := snapshot.(map[string]any)
	if !ok {
		return
	}
	connections, ok := root["connections"].([]any)
	if !ok {
		return
	}
	for _, value := range connections {
		connection, ok := value.(map[string]any)
		if !ok {
			continue
		}
		metadata, _ := connection["metadata"].(map[string]any)
		network := strings.ToLower(stringValue(metadata["network"]))
		if network == "udp" {
			stats.UDP++
		} else {
			stats.TCP++
		}
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

/* CPU 占用率采样环缓冲与按桶聚合，对照 3x-ui 2.9.3 web/service/server.go 的
   AppendCpuSample / AggregateCpuHistory：容量 9000 ≈ 5 小时 @2s，前端 CPU History
   弹窗按 2/30/60/120/180/300 秒桶取最多 60 个平均点。 */

type cpuSample struct {
	T   int64   `json:"t"`   // unix 秒
	Cpu float64 `json:"cpu"` // 百分比 0..100
}

const cpuHistoryCapacity = 9000

var (
	cpuHistoryMu sync.Mutex
	cpuHistory   []cpuSample
)

func appendCPUSample(now time.Time, percent float64) {
	cpuHistoryMu.Lock()
	defer cpuHistoryMu.Unlock()
	sample := cpuSample{T: now.Unix(), Cpu: percent}
	if n := len(cpuHistory); n > 0 && cpuHistory[n-1].T == sample.T {
		cpuHistory[n-1] = sample
		return
	}
	cpuHistory = append(cpuHistory, sample)
	if len(cpuHistory) > cpuHistoryCapacity {
		cpuHistory = cpuHistory[len(cpuHistory)-cpuHistoryCapacity:]
	}
}

func aggregateCPUSHistory(bucketSeconds, maxPoints int) []cpuSample {
	if bucketSeconds <= 0 || maxPoints <= 0 {
		return nil
	}
	cutoff := time.Now().Add(-time.Duration(bucketSeconds*maxPoints) * time.Second).Unix()
	cpuHistoryMu.Lock()
	startIndex := 0
	for i := len(cpuHistory) - 1; i >= 0; i-- {
		if cpuHistory[i].T < cutoff {
			startIndex = i + 1
			break
		}
	}
	if startIndex >= len(cpuHistory) {
		cpuHistoryMu.Unlock()
		return []cpuSample{}
	}
	slice := make([]cpuSample, len(cpuHistory)-startIndex)
	copy(slice, cpuHistory[startIndex:])
	cpuHistoryMu.Unlock()
	var out []cpuSample
	var acc []float64
	bucketSize := int64(bucketSeconds)
	currentBucket := (slice[0].T / bucketSize) * bucketSize
	flush := func(ts int64) {
		if len(acc) == 0 {
			return
		}
		sum := 0.0
		for _, value := range acc {
			sum += value
		}
		out = append(out, cpuSample{T: ts, Cpu: sum / float64(len(acc))})
		acc = acc[:0]
	}
	for _, sample := range slice {
		bucket := (sample.T / bucketSize) * bucketSize
		if bucket != currentBucket {
			flush(currentBucket)
			currentBucket = bucket
		}
		acc = append(acc, sample.Cpu)
	}
	flush(currentBucket)
	if len(out) > maxPoints {
		out = out[len(out)-maxPoints:]
	}
	return out
}
