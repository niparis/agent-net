package protocol

import "testing"

func TestDueArgsValidate(t *testing.T) {
	cases := []struct {
		name string
		args DueArgs
		ok   bool
	}{
		{"time", DueArgs{Action: DueActionAdd, What: "flip", Due: "45m"}, true},
		{"rfc3339", DueArgs{Action: DueActionAdd, What: "flip", Due: "2026-11-21T07:30:00Z"}, true},
		{"after reply", DueArgs{Action: DueActionAdd, What: "flip", After: &DueAfter{Reply: "m_1"}}, true},
		{"due and after", DueArgs{Action: DueActionAdd, What: "flip", Due: "45m", After: &DueAfter{Reply: "m_1"}}, false},
		{"neither", DueArgs{Action: DueActionAdd, What: "flip"}, false},
		{"two after keys", DueArgs{Action: DueActionAdd, What: "flip", After: &DueAfter{Reply: "m_1", AskClosed: "i_1"}}, false},
		{"empty after", DueArgs{Action: DueActionAdd, What: "flip", After: &DueAfter{}}, false},
		{"bad duration", DueArgs{Action: DueActionAdd, What: "flip", Due: "later"}, false},
		{"no what", DueArgs{Action: DueActionAdd, Due: "45m"}, false},
		{"done needs id", DueArgs{Action: DueActionDone}, false},
		{"done", DueArgs{Action: DueActionDone, ID: "d_1"}, true},
		{"list", DueArgs{Action: DueActionList}, true},
		{"unknown action", DueArgs{Action: "snooze"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.args.Validate()
			if (err == nil) != c.ok {
				t.Fatalf("Validate() = %v, want ok %v", err, c.ok)
			}
		})
	}
}
