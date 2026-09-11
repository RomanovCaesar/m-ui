//go:build linux

package app

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	linuxCPUMutex       sync.Mutex
	linuxCPUInitialized bool
	linuxLastTotal      uint64
	linuxLastIdle       uint64
	cpuSpeedMutex       sync.Mutex
	cpuSpeedCached      float64
	cpuSpeedCachedAt    time.Time
)

func collectPlatformStats(dataDir string) SystemStats {
	stats := SystemStats{CPUPercent: linuxCPUPercent(), CPUSpeedMHz: cpuSpeedMHz()}
	if file, err := os.Open("/proc/meminfo"); err == nil {
		values := map[string]uint64{}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 {
				value, _ := strconv.ParseUint(fields[1], 10, 64)
				values[strings.TrimSuffix(fields[0], ":")] = value * 1024
			}
		}
		_ = file.Close()
		stats.MemoryTotal = values["MemTotal"]
		stats.MemoryUsed = values["MemTotal"] - values["MemAvailable"]
		stats.SwapTotal = values["SwapTotal"]
		stats.SwapUsed = values["SwapTotal"] - values["SwapFree"]
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		for index := 0; index < 3 && index < len(fields); index++ {
			stats.Load[index], _ = strconv.ParseFloat(fields[index], 64)
		}
	}
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			uptime, _ := strconv.ParseFloat(fields[0], 64)
			stats.OSUptime = uint64(uptime)
		}
	}
	var filesystem syscall.Statfs_t
	if err := syscall.Statfs(dataDir, &filesystem); err == nil {
		stats.DiskTotal = filesystem.Blocks * uint64(filesystem.Bsize)
		stats.DiskUsed = (filesystem.Blocks - filesystem.Bavail) * uint64(filesystem.Bsize)
	}
	return stats
}

func linuxCPUPercent() float64 {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0
	}
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			break
		}
		values = append(values, value)
	}
	if len(values) < 4 {
		return 0
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	var total uint64
	for _, value := range values {
		total += value
	}
	linuxCPUMutex.Lock()
	defer linuxCPUMutex.Unlock()
	if !linuxCPUInitialized {
		linuxCPUInitialized = true
		linuxLastTotal, linuxLastIdle = total, idle
		return 0
	}
	totalDelta, idleDelta := total-linuxLastTotal, idle-linuxLastIdle
	linuxLastTotal, linuxLastIdle = total, idle
	if totalDelta == 0 || idleDelta > totalDelta {
		return 0
	}
	return float64(totalDelta-idleDelta) / float64(totalDelta) * 100
}

/* 标称频率取 /proc/cpuinfo 第一个 "cpu MHz"，缓存一分钟。 */
func cpuSpeedMHz() float64 {
	cpuSpeedMutex.Lock()
	defer cpuSpeedMutex.Unlock()
	if time.Since(cpuSpeedCachedAt) < time.Minute {
		return cpuSpeedCached
	}
	cpuSpeedCached, cpuSpeedCachedAt = cpuinfoSpeedMHz(), time.Now()
	return cpuSpeedCached
}

func cpuinfoSpeedMHz() float64 {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu MHz") {
			continue
		}
		if index := strings.IndexByte(line, ':'); index >= 0 {
			value, err := strconv.ParseFloat(strings.TrimSpace(line[index+1:]), 64)
			if err == nil {
				return value
			}
		}
	}
	return 0
}
