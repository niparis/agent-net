package daemon

import (
	"errors"
	"os"
	"testing"
)

func stubTimeZone(t *testing.T, linkTarget string, files map[string]string, commands map[string]string, env string) {
	t.Helper()
	oldLink, oldRead, oldRun, oldEnv := timeZoneReadLink, timeZoneReadFile, timeZoneRun, timeZoneGetenv
	t.Cleanup(func() {
		timeZoneReadLink, timeZoneReadFile, timeZoneRun, timeZoneGetenv = oldLink, oldRead, oldRun, oldEnv
	})
	timeZoneReadLink = func(path string) (string, error) {
		if path == "/etc/localtime" && linkTarget != "" {
			return linkTarget, nil
		}
		return "", os.ErrNotExist
	}
	timeZoneReadFile = func(path string) ([]byte, error) {
		if v, ok := files[path]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}
	timeZoneRun = func(name string, args ...string) ([]byte, error) {
		if v, ok := commands[name]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("command not found")
	}
	timeZoneGetenv = func(key string) string {
		if key == "TZ" {
			return env
		}
		return ""
	}
}

func TestZoneFromLinkTarget(t *testing.T) {
	for _, tc := range []struct{ target, want string }{
		{"/usr/share/zoneinfo/Europe/Paris", "Europe/Paris"},
		{"/var/db/timezone/zoneinfo/America/New_York", "America/New_York"},
		{"/usr/share/zoneinfo/UTC", "UTC"},
		{"/etc/localtime", ""},
		{"", ""},
	} {
		if got := zoneFromLinkTarget(tc.target); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.target, got, tc.want)
		}
	}
}

func TestMachineTimeZoneUsesTheLocaltimeLink(t *testing.T) {
	stubTimeZone(t, "/var/db/timezone/zoneinfo/Europe/Paris", map[string]string{"/etc/timezone": "Asia/Tokyo\n"}, map[string]string{"timedatectl": "Asia/Tokyo\n"}, "")
	if got := machineTimeZone(); got != "Europe/Paris" {
		t.Fatalf("got %q", got)
	}
}

func TestMachineTimeZoneFallsBackToTimedatectl(t *testing.T) {
	stubTimeZone(t, "", nil, map[string]string{"timedatectl": "Asia/Tokyo\n"}, "")
	if got := machineTimeZone(); got != "Asia/Tokyo" {
		t.Fatalf("got %q", got)
	}
}

func TestMachineTimeZoneIgnoresSystemSetupRefusal(t *testing.T) {
	stubTimeZone(t, "", nil, map[string]string{"systemsetup": "You need administrator access to run this tool...\n"}, "")
	if got := machineTimeZone(); got != "" {
		t.Fatalf("got %q", got)
	}
	stubTimeZone(t, "", nil, map[string]string{"systemsetup": "Time Zone: America/New_York\n"}, "")
	if got := machineTimeZone(); got != "America/New_York" {
		t.Fatalf("got %q", got)
	}
}

func TestMachineTimeZoneFallsBackToTZ(t *testing.T) {
	stubTimeZone(t, "", nil, nil, "Europe/Berlin")
	if got := machineTimeZone(); got != "Europe/Berlin" {
		t.Fatalf("got %q", got)
	}
}
