package daemon

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// openCodeHelpTimeout bounds the one help read that tells the two opencode
// command shapes apart. A help that hangs must not hold up a judge or a test
// start.
const openCodeHelpTimeout = 10 * time.Second

type openCodeHelp struct {
	mod     time.Time
	size    int64
	variant bool
}

// openCodeHelpCache keeps the answer per binary. worker runs and judge runs
// both ask, and the binary rarely changes; the file's size and time let an
// upgraded binary be read again without comparing versions.
var openCodeHelpCache sync.Map

// openCodeModelFlags names the model and its effort for `opencode run`.
// opencode 2.3.0 removed --variant and puts the effort on the model as
// model#variant; older builds still take --variant. The installed binary's own
// help decides, so an upgrade or a downgrade needs no version list here.
func openCodeModelFlags(ctx context.Context, bin string, selection protocol.ModelSelection) []string {
	model := selection.ModelID()
	if model == "" {
		return nil
	}
	variantFlag := selection.Effort != "" && openCodeTakesVariant(ctx, bin)
	if selection.Effort != "" && !variantFlag {
		model += "#" + selection.Effort
	}
	args := []string{"--model", model}
	if variantFlag {
		args = append(args, "--variant", selection.Effort)
	}
	return args
}

// openCodeTakesVariant reports whether this opencode still lists --variant for
// `opencode run`. A failed or unreadable help means the newer form: it is what
// a current opencode takes, and an older build that answers no help at all
// would fail for other reasons too.
func openCodeTakesVariant(ctx context.Context, bin string) bool {
	var info os.FileInfo
	if fi, err := os.Stat(bin); err == nil {
		info = fi
		if cached, ok := openCodeHelpCache.Load(bin); ok {
			h := cached.(openCodeHelp)
			if h.size == fi.Size() && h.mod.Equal(fi.ModTime()) {
				return h.variant
			}
		}
	}
	hctx, cancel := context.WithTimeout(ctx, openCodeHelpTimeout)
	defer cancel()
	out, _ := exec.CommandContext(hctx, bin, "run", "--help").CombinedOutput()
	takes := strings.Contains(string(out), "--variant")
	if info != nil {
		openCodeHelpCache.Store(bin, openCodeHelp{mod: info.ModTime(), size: info.Size(), variant: takes})
	}
	return takes
}
