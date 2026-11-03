package daemon

import (
	"os"
	"os/exec"
	"strings"
	"time"
)

// These are replaced in tests so no test depends on the machine it runs on.
var (
	timeZoneReadLink = os.Readlink
	timeZoneReadFile = os.ReadFile
	timeZoneRun      = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	}
	timeZoneGetenv = os.Getenv
)

// machineTimeZone reads this machine's IANA time zone, for example
// Europe/Paris. On macOS the answer is the target of /etc/localtime; on Linux
// it is that link, /etc/timezone or timedatectl. It returns "" when it cannot
// tell, and never asks for a password: systemsetup is tried without sudo and
// its refusal is ignored.
func machineTimeZone() string {
	if zone := zoneFromLinkTarget(readLinkOrEmpty("/etc/localtime")); zone != "" {
		return zone
	}
	if zone := loadableZone(readFileOrEmpty("/etc/timezone")); zone != "" {
		return zone
	}
	if out, err := timeZoneRun("timedatectl", "show", "-p", "Timezone", "--value"); err == nil {
		if zone := loadableZone(string(out)); zone != "" {
			return zone
		}
	}
	if out, err := timeZoneRun("systemsetup", "-gettimezone"); err == nil {
		if zone := zoneFromSystemSetup(string(out)); zone != "" {
			return zone
		}
	}
	return loadableZone(timeZoneGetenv("TZ"))
}

func readLinkOrEmpty(path string) string {
	target, err := timeZoneReadLink(path)
	if err != nil {
		return ""
	}
	return target
}

func readFileOrEmpty(path string) string {
	raw, err := timeZoneReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

// zoneFromLinkTarget reads a zone name out of a /etc/localtime link target
// such as /usr/share/zoneinfo/Europe/Paris or
// /var/db/timezone/zoneinfo/Europe/Paris.
func zoneFromLinkTarget(target string) string {
	i := strings.LastIndex(target, "zoneinfo/")
	if i < 0 {
		return ""
	}
	return loadableZone(strings.TrimPrefix(target[i+len("zoneinfo/"):], "/"))
}

// zoneFromSystemSetup reads "Time Zone: Europe/Paris" from systemsetup. Its
// refusal without root says something else and answers "".
func zoneFromSystemSetup(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "Time Zone:"); ok {
			return loadableZone(rest)
		}
	}
	return ""
}

// loadableZone trims a zone name and checks that the database has it.
func loadableZone(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "Local" {
		return ""
	}
	if _, err := time.LoadLocation(name); err != nil {
		return ""
	}
	return name
}
