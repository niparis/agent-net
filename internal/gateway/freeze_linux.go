//go:build linux

package gateway

import (
	"os"
	"strings"
)

func freezeSystemStats() (ioPressure, memPressure string) {
	return readPSI("/proc/pressure/io"), readPSI("/proc/pressure/memory")
}

func readPSI(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "some ") || strings.HasPrefix(line, "full ") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "; ")
}
