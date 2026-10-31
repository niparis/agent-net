package tui

import "time"

// activityBand keeps a session in its old band until the idle margin expires.
func activityBand(now, activity time.Time, previous int, online, urgent bool) int {
	if !online {
		return 4
	}
	if urgent {
		return 0
	}
	age := now.Sub(activity)
	limits := [...]time.Duration{0, 10 * time.Minute, time.Hour, 24 * time.Hour}
	margins := [...]time.Duration{0, 2 * time.Minute, 10 * time.Minute, time.Hour}
	band := 4
	for i := 1; i <= 3; i++ {
		if !activity.IsZero() && age <= limits[i] {
			band = i
			break
		}
	}
	if previous >= 1 && previous <= 3 && band > previous && !activity.IsZero() && age <= limits[previous]+margins[previous] {
		return previous
	}
	return band
}
