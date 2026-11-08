package daemon

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// leadRouteLine is line 44 of internal/gateway/lead_route.go: a SQL string with
// key='cto_session'. The file scan refused it before the fix.
const leadRouteLine = "	err := tx.QueryRow(`SELECT COALESCE(repo_id,''),COALESCE(agent_id,''),harness,worker,COALESCE((SELECT value FROM owner_settings WHERE key='cto_session'),'') FROM sessions WHERE id=?`, m.From).Scan(&senderRepo, &senderAgent, &harness, &worker, &cto)"

// The file scan sends ordinary code that has key, token or auth names in it,
// and refuses a real secret with a generic name, a PEM key or a known token.
func TestPrepareFilesSecretScanTellsCodeFromSecrets(t *testing.T) {
	e := newFileTestEnv(t)
	cases := []struct {
		name, body, secret string
		refused            bool
	}{
		{"go file with key and token names", "package gateway\n\nfunc route() {\n" + leadRouteLine + "\n\ttoken := req.Token\n\tsecret = os.Getenv(\"X\")\n}\n", "", false},
		{"yaml with plain key names", "auth: true\nkey: \"session\"\nmax_tokens: 4096\n", "", false},
		{"generic password in yaml", "password: \"hunter2-Qx7mP2vK9wL4tR8z\"\n", "Qx7mP2vK9wL4tR8z", true},
		{"generic token in go", "const token = \"Qx7mP2vK9wL4tR8zN3bV\"\n", "Qx7mP2vK9wL4tR8zN3bV", true},
		{"pem private key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----\n", "MIIEowIBAAKCAQEA", true},
		{"github token", "token = \"ghp_0123456789abcdefghijklmnopqrstuvwxyz\"\n", "0123456789abcdefghij", true},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeTestFile(t, filepath.Join(e.project, fmt.Sprintf("case-%d.txt", i)), []byte(c.body))
			out, err := PrepareFiles([]string{path}, e.rules(), e.snapshots)
			if !c.refused {
				if err != nil {
					t.Fatalf("an ordinary file was refused: %v", err)
				}
				out.Remove()
				return
			}
			if err == nil || !strings.Contains(err.Error(), "secret") {
				t.Fatalf("a secret was not refused: err=%v", err)
			}
			if strings.Contains(err.Error(), c.secret) {
				t.Fatalf("the refusal repeats the secret: %v", err)
			}
		})
	}
}
