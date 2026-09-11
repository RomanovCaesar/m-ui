//go:build windows

package app

import (
	"encoding/binary"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	windowsKernel32       = syscall.NewLazyDLL("kernel32.dll")
	getSystemTimesProc    = windowsKernel32.NewProc("GetSystemTimes")
	globalMemoryProc      = windowsKernel32.NewProc("GlobalMemoryStatusEx")
	getTickCount64Proc    = windowsKernel32.NewProc("GetTickCount64")
	getDiskFreeSpaceProc  = windowsKernel32.NewProc("GetDiskFreeSpaceExW")
	windowsAdvapi32       = syscall.NewLazyDLL("advapi32.dll")
	regOpenKeyExProc      = windowsAdvapi32.NewProc("RegOpenKeyExW")
	regQueryValueExProc   = windowsAdvapi32.NewProc("RegQueryValueExW")
	regCloseKeyProc       = windowsAdvapi32.NewProc("RegCloseKey")
	windowsCPUMutex       sync.Mutex
	windowsCPUInitialized bool
	windowsLastIdle       uint64
	windowsLastKernel     uint64
	windowsLastUser       uint64
	cpuSpeedMutex         sync.Mutex
	cpuSpeedCached        float64
	cpuSpeedCachedAt      time.Time
)

type windowsFiletime struct {
	Low  uint32
	High uint32
}

type windowsMemoryStatus struct {
	Length            uint32
	MemoryLoad        uint32
	TotalPhysical     uint64
	AvailablePhysical uint64
	TotalPageFile     uint64
	AvailablePageFile uint64
	TotalVirtual      uint64
	AvailableVirtual  uint64
	AvailableExtended uint64
}

func collectPlatformStats(dataDir string) SystemStats {
	stats := SystemStats{}
	stats.CPUPercent = windowsCPUPercent()
	stats.CPUSpeedMHz = cpuSpeedMHz()
	var memory windowsMemoryStatus
	memory.Length = uint32(unsafe.Sizeof(memory))
	if result, _, _ := globalMemoryProc.Call(uintptr(unsafe.Pointer(&memory))); result != 0 {
		stats.MemoryTotal = memory.TotalPhysical
		stats.MemoryUsed = memory.TotalPhysical - memory.AvailablePhysical
		if memory.TotalPageFile > memory.TotalPhysical {
			stats.SwapTotal = memory.TotalPageFile - memory.TotalPhysical
		}
		commitUsed := memory.TotalPageFile - memory.AvailablePageFile
		if commitUsed > stats.MemoryUsed {
			stats.SwapUsed = commitUsed - stats.MemoryUsed
		}
	}
	result, _, _ := getTickCount64Proc.Call()
	stats.OSUptime = uint64(result) / 1000
	root := filepath.VolumeName(dataDir) + `\`
	rootPointer, _ := syscall.UTF16PtrFromString(root)
	var available, total, free uint64
	if success, _, _ := getDiskFreeSpaceProc.Call(uintptr(unsafe.Pointer(rootPointer)), uintptr(unsafe.Pointer(&available)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free))); success != 0 {
		stats.DiskTotal = total
		stats.DiskUsed = total - free
	}
	return stats
}

func windowsCPUPercent() float64 {
	var idle, kernel, user windowsFiletime
	result, _, _ := getSystemTimesProc.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if result == 0 {
		return 0
	}
	idleValue := uint64(idle.High)<<32 | uint64(idle.Low)
	kernelValue := uint64(kernel.High)<<32 | uint64(kernel.Low)
	userValue := uint64(user.High)<<32 | uint64(user.Low)
	windowsCPUMutex.Lock()
	defer windowsCPUMutex.Unlock()
	if !windowsCPUInitialized {
		windowsCPUInitialized = true
		windowsLastIdle, windowsLastKernel, windowsLastUser = idleValue, kernelValue, userValue
		return 0
	}
	idleDelta := idleValue - windowsLastIdle
	totalDelta := kernelValue - windowsLastKernel + userValue - windowsLastUser
	windowsLastIdle, windowsLastKernel, windowsLastUser = idleValue, kernelValue, userValue
	if totalDelta == 0 || idleDelta > totalDelta {
		return 0
	}
	return float64(totalDelta-idleDelta) / float64(totalDelta) * 100
}

/*
标称频率读注册表 CentralProcessor\0 的 ~MHz（DWORD），和任务管理器同源；

	值只在启动时写入，缓存一分钟足够，没必要每次轮询都进注册表。
*/
func cpuSpeedMHz() float64 {
	cpuSpeedMutex.Lock()
	defer cpuSpeedMutex.Unlock()
	if time.Since(cpuSpeedCachedAt) < time.Minute {
		return cpuSpeedCached
	}
	cpuSpeedCached, cpuSpeedCachedAt = registryCPUSpeedMHz(), time.Now()
	return cpuSpeedCached
}

func registryCPUSpeedMHz() float64 {
	const keyRead = 0x20019
	keyPath, err := syscall.UTF16PtrFromString(`HARDWARE\DESCRIPTION\System\CentralProcessor\0`)
	if err != nil {
		return 0
	}
	valueName, err := syscall.UTF16PtrFromString("~MHz")
	if err != nil {
		return 0
	}
	var key syscall.Handle
	if result, _, _ := regOpenKeyExProc.Call(uintptr(syscall.HKEY_LOCAL_MACHINE), uintptr(unsafe.Pointer(keyPath)), 0, keyRead, uintptr(unsafe.Pointer(&key))); result != 0 {
		return 0
	}
	defer regCloseKeyProc.Call(uintptr(key))
	var valueType, size uint32
	var buffer [4]byte
	size = uint32(len(buffer))
	if result, _, _ := regQueryValueExProc.Call(uintptr(key), uintptr(unsafe.Pointer(valueName)), 0, uintptr(unsafe.Pointer(&valueType)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size))); result != 0 {
		return 0
	}
	return float64(binary.LittleEndian.Uint32(buffer[:]))
}
