package delivery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewFindingWorktreeConfig(t *testing.T) {
	f := newConfigFx(t)
	req := f.req("feat", f.head, MergeModeCommit)
	req.Push = false
	if _, err := Merge(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	mergeGit(t, f.repo, "config", "extensions.worktreeConfig", "true")
	mergeGit(t, f.wt, "config", "--worktree", "filter.evil.smudge", f.touchCmd()+"; cat")
	_, err := Merge(context.Background(), req)
	if _, statErr := os.Stat(f.marker); statErr == nil {
		t.Errorf("worktree filter executed")
	}
	if !errors.Is(err, ErrUnsafeConfig) {
		t.Errorf("got %v; want ErrUnsafeConfig", err)
	}
}

func TestReviewFindingRedirect(t *testing.T) {
	f := newConfigFx(t)
	alt := filepath.Join(f.root, "alt.git")
	mergeGit(t, f.root, "clone", "-q", "--bare", f.origin, alt)
	if err := os.WriteFile(filepath.Join(alt, "hooks", "pre-receive"), []byte("#!/bin/sh\ntouch "+f.marker+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	f.setConfig("remote.origin.pushurl", alt)
	_, err := Merge(context.Background(), f.req("feat", f.head, MergeModeCommit))
	if _, statErr := os.Stat(f.marker); statErr == nil {
		t.Errorf("redirected local remote hook executed")
	}
	if err == nil {
		t.Error("redirected push succeeded")
	}
}

func TestReviewFindingCoreWorktree(t *testing.T) {
	f := newMergeFx(t)
	clean := filepath.Join(f.root, "clean-decoy")
	if err := os.MkdirAll(clean, 0755); err != nil {
		t.Fatal(err)
	}
	f.write(clean, "README", "base\n")
	mergeGit(t, f.repo, "config", "core.worktree", clean)
	f.write(f.repo, "README", "dirty payload\n")
	res, err := RunStep(context.Background(), StepCommand{Step: "release", Dir: f.repo, Commit: f.base, Argv: []string{"cat", "README"}})
	if err == nil {
		t.Errorf("dirty checkout accepted; output %q", res.OutputTail)
	}
}

func TestReviewFindingConfigRace(t *testing.T) {
	f := newConfigFx(t)
	binDir := filepath.Join(f.root, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(f.root, "config-count")
	wrapper := fmt.Sprintf(`#!/bin/sh
/usr/bin/git "$@"
status=$?
case " $* " in
  *" config --list --show-scope --includes -z "*)
    n=$(cat %q 2>/dev/null || printf 0)
    n=$((n+1))
    printf '%%s' "$n" > %q
    if [ "$n" = 2 ]; then /usr/bin/git -C %q config --local filter.evil.smudge %q; fi
    ;;
esac
exit "$status"
`, countFile, countFile, f.repo, f.touchCmd()+"; cat")
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(wrapper), 0755); err != nil {
		t.Fatal(err)
	}
	req := f.req("feat", f.head, MergeModeCommit)
	req.Push = false
	req.GitPath = binDir + ":/usr/bin:/bin"
	_, err := Merge(context.Background(), req)
	if _, statErr := os.Stat(f.marker); statErr == nil {
		t.Errorf("filter executed after config changed following check; Merge err=%v", err)
	}
}
