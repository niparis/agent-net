package protocol

import (
	"fmt"
	"strings"
	"time"

	// The zone database travels in the binary, so an IANA name loads on a
	// machine without zoneinfo files (the owner's display time zone).
	_ "time/tzdata"
)

// TimeZoneAuto tells the display to pick the zone of the owner's own machine.
const TimeZoneAuto = "auto"

const (
	// OpTimeZoneGet reads the owner's display time zone for the whole network.
	OpTimeZoneGet = "timezone_get"
	// OpTimeZoneSet writes it. Owner only: no agent tool changes it.
	OpTimeZoneSet = "timezone_set"
)

// TimeZoneSettings is the owner's display time zone. Name is "auto" (the zone
// of the owner's own machine) or an IANA name such as Europe/Paris.
type TimeZoneSettings struct {
	Name string `json:"name"`
}

// NormalizeTimeZone checks an owner's display time zone. An empty name means
// auto. Any other name must load from the zone database.
func NormalizeTimeZone(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, TimeZoneAuto) {
		return TimeZoneAuto, nil
	}
	if _, err := time.LoadLocation(name); err != nil {
		return "", fmt.Errorf("unknown time zone %q: use auto or a name like Europe/Paris", name)
	}
	return name, nil
}
