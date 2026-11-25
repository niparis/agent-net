//go:build linux

package gateway

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func freezeSystemStats() (ioPressure, memInfo string) {
	if raw, err := os.ReadFile("/proc/pressure/io"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "some ") {
				ioPressure = strings.TrimSpace(line)
				break
			}
		}
	}
	var totalKB, availKB uint64
	if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			switch fields[0] {
			case "MemTotal:":
				totalKB, _ = strconv.ParseUint(fields[1], 10, 64)
			case "MemAvailable:":
				availKB, _ = strconv.ParseUint(fields[1], 10, 64)
			}
		}
	}
	var rssMB uint64
	if raw, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) >= 2 {
			pages, _ := strconv.ParseUint(fields[1], 10, 64)
			rssMB = (pages * uint64(os.Getpagesize())) >> 20
		}
	}
	if totalKB > 0 || availKB > 0 || rssMB > 0 {
		memInfo = fmt.Sprintf("avail=%dMB total=%dMB rss=%dMB", availKB>>10, totalKB>>10, rssMB)
	}
	return ioPressure, memInfo
}
