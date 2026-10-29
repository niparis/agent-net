package gateway

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// formatLine renders one log line: ts=... event=... key=value ... Pairs with
// an empty value are left out. A value that holds a space, quote, "=" or a
// control character is quoted, so one event is always one line and a name
// chosen by an agent cannot fake another field.
//
// Message bodies are never passed here.
func formatLine(ts time.Time, event string, kv ...any) string {
	return "ts=" + ts.UTC().Format("2006-01-02T15:04:05.000Z") + " event=" + quoteValue(event) + pairs(kv...)
}

// pairs renders key=value pairs, each with a space before it.
func pairs(kv ...any) string {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		v := fmt.Sprint(kv[i+1])
		if v == "" {
			continue
		}
		b.WriteByte(' ')
		b.WriteString(fmt.Sprint(kv[i]))
		b.WriteByte('=')
		b.WriteString(quoteValue(v))
	}
	return b.String()
}

func quoteValue(v string) string {
	for _, r := range v {
		if r == ' ' || r == '"' || r == '=' || r == '\\' || !unicode.IsPrint(r) {
			return strconv.Quote(v)
		}
	}
	return v
}

// logLine writes one structured line to the gateway log. It is also the hook
// the store uses for the events it records.
func (g *Gateway) logLine(event string, kv ...any) {
	g.logger.Print(formatLine(g.store.now(), event, kv...))
}
