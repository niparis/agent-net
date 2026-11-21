package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const timeZoneSettingsKey = "owner.time_zone"

// timeZoneSetting reads the owner's display time zone. The default is auto:
// the zone of the owner's own machine.
func (s *Store) timeZoneSetting() (protocol.TimeZoneSettings, error) {
	var name string
	set, err := ownerJSON(s.db, timeZoneSettingsKey, &name)
	if err != nil {
		return protocol.TimeZoneSettings{}, err
	}
	if !set {
		return protocol.TimeZoneSettings{Name: protocol.TimeZoneAuto}, nil
	}
	clean, err := protocol.NormalizeTimeZone(name)
	if err != nil {
		// A value written by hand and not a zone name means auto.
		return protocol.TimeZoneSettings{Name: protocol.TimeZoneAuto}, nil
	}
	return protocol.TimeZoneSettings{Name: clean}, nil
}

// ownerUTCNames are the zone names that carry no owner location. A machine
// that reports one does not tell the schedule where the owner is.
var ownerUTCNames = map[string]bool{
	"UTC": true, "Etc/UTC": true, "GMT": true, "Etc/GMT": true,
	"Universal": true, "Zulu": true, "Etc/Universal": true, "Etc/Zulu": true,
}

// ownerTimeZone resolves the zone the owner's schedule uses. A named setting
// wins. auto takes a zone a machine reported, preferring the owner's Mac.
// When no machine reported a usable zone it says false: the caller must wait,
// never stand in UTC for a zone nobody reported.
func (s *Store) ownerTimeZone() (string, bool, error) {
	tz, err := s.timeZoneSetting()
	if err != nil {
		return "", false, err
	}
	if tz.Name != protocol.TimeZoneAuto {
		if _, err := time.LoadLocation(tz.Name); err != nil {
			return "", false, nil
		}
		return tz.Name, true, nil
	}
	nodes, err := s.Nodes()
	if err != nil {
		return "", false, err
	}
	for _, wantMac := range []bool{true, false} {
		for _, n := range nodes {
			if wantMac && n.OS != "darwin" {
				continue
			}
			zone := strings.TrimSpace(n.TimeZone)
			if zone == "" || ownerUTCNames[zone] {
				continue
			}
			if _, err := time.LoadLocation(zone); err == nil {
				return zone, true, nil
			}
		}
	}
	return "", false, nil
}

func (g *Gateway) handleTimeZoneSettings(w http.ResponseWriter, r *http.Request) {
	node, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method == http.MethodGet {
		out, err := g.store.timeZoneSetting()
		writeJSON(w, out, err)
		return
	}
	var in protocol.TimeZoneSettings
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad time zone setting", 400)
		return
	}
	name, err := protocol.NormalizeTimeZone(in.Name)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	err = g.store.setOwner(timeZoneSettingsKey, name)
	if err == nil {
		g.store.Event("timezone.set", node, "", "", name)
	}
	writeJSON(w, protocol.TimeZoneSettings{Name: name}, err)
}
