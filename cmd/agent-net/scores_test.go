package main

import "testing"

func TestScoreArguments(t *testing.T) {
	for _, tt := range []struct {
		args  []string
		valid bool
	}{
		{nil, true}, {[]string{"set", "model", "32"}, true}, {[]string{"clear", "model"}, true}, {[]string{"set", "model", "101"}, false}, {[]string{"set", "model", "nan"}, false}, {[]string{"clear"}, false},
	} {
		_, _, err := scoreArguments(tt.args)
		if (err == nil) != tt.valid {
			t.Errorf("%v: %v", tt.args, err)
		}
	}
}
