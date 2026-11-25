//go:build !linux

package gateway

func freezeSystemStats() (ioPressure, memInfo string) {
	return "", ""
}
