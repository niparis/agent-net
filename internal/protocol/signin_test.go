package protocol

import (
	"reflect"
	"testing"
)

// The allowlist is the only place a login command comes from. A quota provider
// id may carry a suffix; it maps to the key, never to a new command.
func TestSignInProviderAllowlist(t *testing.T) {
	for _, id := range []string{"alibaba", "Alibaba", "alibaba-bl", "alibaba_cloud"} {
		key, ok := SignInProviderKey(id)
		if !ok || key != "alibaba" {
			t.Fatalf("%q -> %q %v", id, key, ok)
		}
	}
	info, ok := SignInProvider("alibaba")
	if !ok || !reflect.DeepEqual(info.Argv, []string{"bl", "auth", "login", "--console"}) || info.Label != "Alibaba Bailian CLI" {
		t.Fatalf("alibaba allowlist: %+v %v", info, ok)
	}
	for _, bad := range []string{"", "sh", "bl auth login --console", "alibaba; rm -rf /"} {
		if _, ok := SignInProvider(bad); ok {
			t.Errorf("unknown provider %q was accepted", bad)
		}
	}
	if !SignInNeeded("alibaba", "No console access token found.") ||
		!SignInNeeded("alibaba", "hint: Run `bl auth login --console`") ||
		SignInNeeded("alibaba", "rate limited") ||
		SignInNeeded("claude", "No console access token found.") {
		t.Fatal("sign-in remedy detection is wrong")
	}
}
