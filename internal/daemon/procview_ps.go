package daemon

import (
	"strconv"
	"strings"
	"time"
)

// psTableFormat is the ps column list that parsePSLine reads: the process ID,
// parent, effective user, state, the five words of the start time, the
// resident set size in kilobytes, and the rest of the line as the command.
const psTableFormat = "pid=,ppid=,uid=,stat=,lstart=,rss=,command="

// psStartLayout is the lstart column of ps, as time.Parse reads it.
const psStartLayout = "Mon Jan _2 15:04:05 2006"

// parsePSLine reads one line of `ps -A -ww -o psTableFormat`. It returns false
// for a line that is not a process.
func parsePSLine(line string) (procView, bool) {
	words, command := splitWords(line, 10)
	if len(words) < 10 {
		return procView{}, false
	}
	pid, err1 := strconv.Atoi(words[0])
	ppid, err2 := strconv.Atoi(words[1])
	uid, err3 := strconv.Atoi(words[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return procView{}, false
	}
	v := procView{PID: pid, PPID: ppid, UID: uid, EUID: uid} // ps prints the effective user
	v.Zombie = strings.HasPrefix(words[3], "Z")
	start := strings.Join(words[4:9], " ")
	v.Token = start
	if t, err := time.ParseInLocation(psStartLayout, start, time.Local); err == nil {
		v.Started = t.UTC()
	}
	v.RSSKB, _ = strconv.ParseInt(words[9], 10, 64)
	v.Command = command
	if v.Command == "" {
		v.Command = "[unknown]"
	}
	return v, true
}

// splitWords returns the first n blank-separated words of line, and the rest of
// the line with its leading blanks removed. The rest may hold blanks itself.
func splitWords(line string, n int) ([]string, string) {
	var words []string
	rest := strings.TrimSpace(line)
	for len(words) < n && rest != "" {
		i := strings.IndexAny(rest, " \t")
		if i < 0 {
			i = len(rest)
		}
		words = append(words, rest[:i])
		rest = strings.TrimLeft(rest[i:], " \t")
	}
	return words, rest
}
