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

func TestHasSecretIgnoresOrdinaryCodeAndConfiguration(t *testing.T) {
	in := `if (e.key === 'Escape') {
  var STORAGE_KEY = 'x';
  var ownerToken = null;
  var token = new RegExp('token');
  var recordsByKey = {};
  return min(matches, key=lambda p: len(p.parts))
  key=lambda p: p.stat().st_mtime, reverse=True)
  sorted(items, key=len)
}`
	if HasSecret(in) {
		t.Fatal("ordinary JavaScript and Python was detected as a secret")
	}
}

func TestHasSecretIgnoresDisplayOnlyHeuristics(t *testing.T) {
	for _, in := range []string{
		"api_key=ordinary-value",
		"Authorization: Bearer ordinary-value",
		"bearer ordinary-value",
		"command --token ordinary-value",
	} {
		if HasSecret(in) {
			t.Errorf("HasSecret(%q) = true, want it ignored", in)
		}
	}
}

func TestHasSecretFindsHighConfidenceShapes(t *testing.T) {
	for _, in := range []string{
		"OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwx",
		"ghp_0123456789abcdefghij",
		"github_pat_0123456789abcdefghij",
		"AKIAIOSFODNN7EXAMPLE",
		"xoxb-1234567890-abcd",
		"AIza01234567890123456789012345678901234",
		"glpat-0123456789abcdefghij",
		"eyJabcdefgh.abcdefgh.abcdefgh",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
		"https://alice:secretpassword@example.test/path",
	} {
		if !HasSecret(in) {
			t.Errorf("HasSecret(%q) = false, want a recognized secret", in)
		}
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
		"return min(matches, key=lambda p: len(p.parts))",
		"key=lambda p: p.stat().st_mtime, reverse=True)",
		"sorted(items, key=len)",
		"key: value",
		`{"key": "id"}`,
		"auth=None",
		"auth: required",
		"",
	} {
		if got := Secrets(in); got != in {
			t.Errorf("Secrets(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestSecretsMasksCompoundSensitiveNames(t *testing.T) {
	for _, in := range []string{
		"api_key=plainvalue123",
		"apiKey=plainvalue123",
		"API-KEY=plainvalue123",
		"secret_key=plainvalue123",
		"private_key=plainvalue123",
		"access_key=plainvalue123",
		"ssh_key=plainvalue123",
		"MY_APP_KEY=plainvalue123",
		"signing.key=plainvalue123",
		"secret=plainvalue123",
		"token=plainvalue123",
		"password=plainvalue123",
		"credentials=plainvalue123",
		"SERVICE_AUTH=plainvalue123",
	} {
		got := Secrets(in)
		if strings.Contains(got, "plainvalue123") || !strings.Contains(got, Mask) {
			t.Errorf("Secrets(%q) = %q, want value masked", in, got)
		}
	}
}

func TestSecretsDoesNotMaskWordsEndingInSk(t *testing.T) {
	for _, in := range []string{
		"- [What lands on disk: state the contract](#what-lands-on-disk-state-the-contract)",
		"task-assignment-and-routing-rules",
		"risk-assessment-for-the-quarter",
		"a desk-booking-system-for-teams",
	} {
		if got := Secrets(in); got != in {
			t.Errorf("Secrets(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestSecretsMasksSkKeysAtTokenBoundaries(t *testing.T) {
	for _, in := range []string{
		"OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwx",
		"sk-proj-abcdefghijklmnop1234",
		"key: sk-ant-api03-abcdefghijklmnopqrst",
		"sk-abcdefghijklmnopqrstuvwx",
		" sk-abcdefghijklmnopqrstuvwx",
		`"sk-abcdefghijklmnopqrstuvwx`,
		"=sk-abcdefghijklmnopqrstuvwx",
		"(sk-abcdefghijklmnopqrstuvwx",
	} {
		got := Secrets(in)
		if strings.Contains(got, "sk-") || strings.Contains(got, "abcdefghijklmnop") {
			t.Errorf("Secrets(%q) = %q, want key masked", in, got)
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
