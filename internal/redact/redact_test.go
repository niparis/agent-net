package redact

import (
	"strings"
	"testing"
)

func TestSecretsMasksCommonShapes(t *testing.T) {
	cases := []struct{ name, in, secret string }{
		{"openai key", "use sk-abcdefghijklmnopqrstuvwxyz123456 now", "abcdefghijklmnopqrstuvwxyz123456"},
		{"anthropic key", "key sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz_0123456789-abc", "AbCdEfGhIjKlMnOpQrStUv"},
		{"github token", "git push https with ghp_0123456789abcdefghijklmnopqrstuvwxyz", "0123456789abcdefghij"},
		{"github pat", "github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz", "11ABCDEFG0123456789"},
		{"aws key id", "AWS id AKIAIOSFODNN7EXAMPLE here", "IOSFODNN7EXAMPLE"},
		{"slack", "xoxb-1234567890-abcdefghij", "1234567890-abcdefghij"},
		{"bearer header", "curl -H 'Authorization: Bearer abc123def456ghi789' https://x", "abc123def456ghi789"},
		{"bearer text", "send bearer zzzzzzzzzzzzzzzz1234", "zzzzzzzzzzzzzzzz1234"},
		{"token equals long hex", "token=0123456789abcdef0123456789abcdef01234567", "0123456789abcdef0123456789abcdef01234567"},
		{"key equals base64", "api_key=QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=", "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo="},
		{"env style", "OPENAI_API_KEY=hunter2hunter2 node app.js", "hunter2hunter2"},
		{"json style", `{"password": "correct horse battery"}`, "correct horse battery"},
		{"yaml style", "secret: s3cr3tvalue", "s3cr3tvalue"},
		{"password flag", "mysql --password=topsecret123 -h db", "topsecret123"},
		{"url credentials", "git clone https://bob:hunter22@example.com/r.git", "hunter22"},
		{"jwt", "jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk", "eyJzdWIiOiIxMjM0NTY3ODkwIn0"},
		{"private key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----", "MIIEowIBAAKCAQEA"},
		{"authorization basic", "Authorization: Basic dXNlcjpwYXNz", "dXNlcjpwYXNz"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Secrets(c.in)
			if strings.Contains(got, c.secret) {
				t.Fatalf("secret %q survived: %q", c.secret, got)
			}
			if !strings.Contains(got, Mask) {
				t.Fatalf("no mask in %q", got)
			}
		})
	}
}

func TestSecretsLeavesOrdinaryTextAlone(t *testing.T) {
	for _, in := range []string{
		"go test ./... -race",
		"max_tokens: 4096",
		"the skill is sk-short",
		"npm install --save-dev typescript",
		"ls -la /home/user/code",
		"tokenizer.encode(text)",
		"",
	} {
		if got := Secrets(in); got != in {
			t.Errorf("Secrets(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestArgsMasksFlagsAndCuts(t *testing.T) {
	got := Args("deploy --token abc123secret --env prod --password=hunter2")
	for _, s := range []string{"abc123secret", "hunter2"} {
		if strings.Contains(got, s) {
			t.Errorf("%q leaked in %q", s, got)
		}
	}
	if !strings.Contains(got, "deploy") || !strings.Contains(got, "--env prod") {
		t.Errorf("lost the harmless part: %q", got)
	}
	long := "run " + strings.Repeat("x", 200)
	if n := len([]rune(Args(long))); n != ArgsLen {
		t.Errorf("length = %d, want %d", n, ArgsLen)
	}
}

func TestArgsMasksBeforeCutting(t *testing.T) {
	// The secret straddles the 80 character cut. If the cut came first, half of it would stay.
	in := strings.Repeat("a", 70) + " sk-abcdefghijklmnopqrstuvwxyz0123456789"
	got := Args(in)
	if strings.Contains(got, "sk-") || strings.Contains(got, "abcdefghij") {
		t.Errorf("part of the secret leaked: %q", got)
	}
}

func TestArgsStripsControlCharacters(t *testing.T) {
	got := Args("echo\x1b[31m hi\x00there\nnext")
	for _, r := range got {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("control character in %q", got)
		}
	}
}

func TestCutCountsCharacters(t *testing.T) {
	if got := Cut("héllo wörld", 5); got != "héllo" {
		t.Errorf("Cut = %q", got)
	}
	if got := Cut("abc", 10); got != "abc" {
		t.Errorf("Cut = %q", got)
	}
}
