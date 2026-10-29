package harnesses

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DefaultDirs are the usual places where these tools are installed. home is
// the home folder; "" means the user's. A daemon started by systemd or launchd
// does not read ~/.bashrc, so the folders of Node version managers (nvm, fnm,
// volta, asdf, mise), pnpm and bun are listed here.
func DefaultDirs(home string) []string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	dirs := []string{}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".opencode", "bin"),
		)
		dirs = append(dirs, versionManagerDirs(home)...)
	}
	return append(dirs, "/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin")
}

func versionManagerDirs(home string) []string {
	var dirs []string
	add := func(d ...string) {
		for _, x := range d {
			if x != "" && filepath.IsAbs(x) {
				dirs = append(dirs, x)
			}
		}
	}
	env := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }

	// nvm: $NVM_DIR first, then ~/.nvm.
	for _, root := range uniq(env("NVM_DIR"), filepath.Join(home, ".nvm")) {
		add(nvmBins(root)...)
	}
	// fnm.
	for _, root := range uniq(env("FNM_DIR"),
		filepath.Join(home, ".local", "share", "fnm"),
		filepath.Join(home, ".fnm"),
		filepath.Join(home, "Library", "Application Support", "fnm")) {
		add(fnmBins(root)...)
	}
	// volta, asdf, mise shims.
	add(filepath.Join(orDefault(env("VOLTA_HOME"), filepath.Join(home, ".volta")), "bin"))
	add(filepath.Join(orDefault(env("ASDF_DATA_DIR"), filepath.Join(home, ".asdf")), "shims"))
	add(filepath.Join(home, ".local", "share", "mise", "shims"), filepath.Join(home, ".mise", "shims"))
	// pnpm global.
	add(env("PNPM_HOME"), filepath.Join(home, ".local", "share", "pnpm"), filepath.Join(home, "Library", "pnpm"))
	// bun.
	add(filepath.Join(orDefault(env("BUN_INSTALL"), filepath.Join(home, ".bun")), "bin"))
	return dirs
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func uniq(s ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range s {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// installedVersions lists the names of the "v..." folders in dir, newest first.
func installedVersions(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var vers []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
			vers = append(vers, e.Name())
		}
	}
	sort.Slice(vers, func(i, j int) bool { return versionLess(vers[j], vers[i]) })
	return vers
}

// toFront moves v to the front of vers, if it is there.
func toFront(vers []string, v string) []string {
	out := make([]string, 0, len(vers))
	found := false
	for _, x := range vers {
		if x == v {
			found = true
		} else {
			out = append(out, x)
		}
	}
	if !found {
		return vers
	}
	return append([]string{v}, out...)
}

// nvmBins lists <root>/versions/node/*/bin. The version that nvm's default
// alias names comes first (when it resolves), then the others, newest first.
func nvmBins(root string) []string {
	vers := installedVersions(filepath.Join(root, "versions", "node"))
	if def := nvmResolve(root, "default", vers, 0); def != "" {
		vers = toFront(vers, def)
	}
	var out []string
	for _, v := range vers {
		out = append(out, filepath.Join(root, "versions", "node", v, "bin"))
	}
	return out
}

// nvmResolve follows nvm aliases ("default" -> "lts/iron" -> "v20.11.0" or a
// prefix like "22") to one installed version name. vers is sorted newest first.
// It returns "" when nothing matches.
func nvmResolve(root, name string, vers []string, depth int) string {
	if depth > 5 || strings.Contains(name, "..") {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(root, "alias", filepath.FromSlash(name)))
	if err != nil {
		return ""
	}
	val := strings.TrimSpace(string(b))
	if val == "" {
		return ""
	}
	switch val {
	case "node", "stable":
		if len(vers) > 0 {
			return vers[0]
		}
		return ""
	}
	if c := val[0]; c == 'v' || (c >= '0' && c <= '9') {
		prefix := val
		if c != 'v' {
			prefix = "v" + val
		}
		for _, v := range vers {
			if v == prefix || strings.HasPrefix(v, prefix+".") {
				return v
			}
		}
		return ""
	}
	return nvmResolve(root, val, vers, depth+1) // another alias, like lts/iron
}

// fnmBins lists <root>/node-versions/*/installation/bin. The version behind
// the "default" alias (a symlink) comes first, then the others, newest first.
func fnmBins(root string) []string {
	base := filepath.Join(root, "node-versions")
	vers := installedVersions(base)
	if t, err := filepath.EvalSymlinks(filepath.Join(root, "aliases", "default")); err == nil {
		// The target is <base>/<version>/installation.
		vers = toFront(vers, filepath.Base(filepath.Dir(t)))
	}
	var out []string
	for _, v := range vers {
		out = append(out, filepath.Join(base, v, "installation", "bin"))
	}
	return out
}

// versionLess compares "v22.3.1" style names numerically.
func versionLess(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func versionParts(v string) []int {
	var out []int
	for _, s := range strings.Split(strings.TrimPrefix(v, "v"), ".") {
		n, _ := strconv.Atoi(s)
		out = append(out, n)
	}
	return out
}
