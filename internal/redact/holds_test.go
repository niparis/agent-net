package redact

import (
	"strings"
	"testing"
)

// leadRouteLine is line 44 of internal/gateway/lead_route.go as it is in the
// tree. Its SQL has key='cto_session', a plain word in a string, not a secret.
const leadRouteLine = "	err := tx.QueryRow(`SELECT COALESCE(repo_id,''),COALESCE(agent_id,''),harness,worker,COALESCE((SELECT value FROM owner_settings WHERE key='cto_session'),'') FROM sessions WHERE id=?`, m.From).Scan(&senderRepo, &senderAgent, &harness, &worker, &cto)"

func TestHoldsSecretAllowsOrdinaryCode(t *testing.T) {
	cases := []struct{ name, in string }{
		{"sql key compared to a word", leadRouteLine},
		{"sql key in a short quoted word", "WHERE key='cto_session'"},
		{"token read from a request", "token := req.Token"},
		{"auth flag", "auth: true"},
		{"key with a short word", `key: "session"`},
		{"secret from the environment", `secret = os.Getenv("X")`},
		{"token from the environment", `token = os.Getenv("OPENAI_API_TOKEN_2")`},
		{"shell variable", "password: ${DB_PASSWORD}"},
		{"template", `token: "{{ .Token }}"`},
		{"angle bracket placeholder", `api_key: "<your-api-key-here>"`},
		{"dotted config path", "secret = config.Secrets.DatabasePassword2"},
		{"identifier with a version", "token: cto_session_v2_handler"},
		{"yaml anchor to an identifier", "token: &tokenDefaultValue2"},
		{"file path after a pwd name", "PWD=/tmp/agent-net-difficulty-opencode-9f8e7d6c5b4a"},
		{"home path after a token name", "token: ~/config/session-token-v2-cache"},
		{"hyphenated phrase with a number", `secret = "value-that-must-never-move-773"`},
		{"camel case identifier with a digit", `token: "userV2SessionTokenHandler"`},
		{"upper case env name with a digit", "secret: MY_APP_API_KEY_2"},
		{"no digit placeholder", `key: "xxxxxxxxxxxxxxxxxxxxxxxx"`},
		{"upper case placeholder", `password: "CHANGE_ME_TO_A_LONG_VALUE"`},
		{"one character repeated", `key: "a1a1a1a1a1a1a1a1a1"`},
		{"go authorization assignment", "authorization := r.Header.Get(\"Authorization\")"},
		{"go header set with bearer prefix", `req.Header.Set("Authorization", "Bearer "+tok)`},
		{"prose about bearer tokens", "The bearer token is sent in the header."},
		{"tokenizer call", "tokenizer.encode(text)"},
		{"short value", "max_tokens: 4096"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if HoldsSecret(c.in) {
				t.Fatalf("HoldsSecret(%q) = true, want the line allowed", c.in)
			}
		})
	}
}

func TestHoldsSecretRefusesStrongShapes(t *testing.T) {
	cases := []struct{ name, in string }{
		{"aws key id", `key = "AKIAIOSFODNN7EXAMPLE"`},
		{"openai style key", "api_key=sk-Qx7mP2vK9wL4tR8zN3bV6cD1"},
		{"github token", `token = "ghp_0123456789abcdefghijklmnopqrstuvwxyz"`},
		{"jwt", "jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"},
		{"pem private key block", "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----"},
		{"openssh key block", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjE=\n-----END OPENSSH PRIVATE KEY-----"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !HoldsSecret(c.in) {
				t.Fatalf("HoldsSecret(%q) = false, want the secret refused", c.in)
			}
		})
	}
}

func TestHoldsSecretRefusesGenericValues(t *testing.T) {
	cases := []struct{ name, in string }{
		{"password in yaml", `password: "hunter2-Qx7mP2vK9wL4tR8z"`},
		{"long password with words", `password: "hunter2-long-random-Xy8kQ2vL9mZ4pR7tW"`},
		{"token in go", `const token = "Qx7mP2vK9wL4tR8zN3bV"`},
		{"hex secret", "secret: 9f8e7d6c5b4a3210fedcba9876543210"},
		{"json api key", `{"api_key": "Qx7mP2vK9wL4tR8zN3bV"}`},
		{"env style", "OPENAI_API_KEY=Qx7mP2vK9wL4tR8zN3bV node app.js"},
		{"password flag", "mysql --password=Qx7mP2vK9wL4tR8z -h db"},
		{"url credentials", "git clone https://bob:Qx7mP2vK9wL4tR8z@example.com/r.git"},
		{"bearer header", "curl -H 'Authorization: Bearer Qx7mP2vK9wL4tR8z' https://x"},
		{"password with symbols", `password: "Tr0ub4dor&3xKcd7!"`},
		{"base64 jwt header", `token: "eyJhbGciOiJIUzI1NiJ9"`},
		{"base64 key", `api_key: "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo="`},
		{"hex with a separator", `secret: "abcdef_0123456789abcdef"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !HoldsSecret(c.in) {
				t.Fatalf("HoldsSecret(%q) = false, want the value refused", c.in)
			}
		})
	}
}

// The logs keep the broad rule. A line that the file scan allows can still be
// masked in a log.
func TestSecretsStillMasksWhatTheFileScanAllows(t *testing.T) {
	got := Secrets(leadRouteLine)
	if got == leadRouteLine || !strings.Contains(got, Mask) {
		t.Fatalf("Secrets changed for logs: %q", got)
	}
}
