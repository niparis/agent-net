package graphview

// The kitty graphics protocol can carry a real image behind the Graph tab.
// The image is transmitted once with U=1 and then shown through U+10EEEE
// placeholder cells. Placeholders are ordinary text, so they survive a
// line-based redraw. Everywhere else - and whenever the terminal does not
// answer - the Braille renderer is used.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// ImageID is the id of the Graph image. It fits in the 24-bit foreground
// colour that a Unicode placeholder uses to name its image.
const ImageID uint32 = 0x4E4554

const placeholder = '\U0010EEEE'

// diacritics maps a row or column number to the combining mark kitty expects
// in a placeholder cell. The order is from kitty's rowcolumn-diacritics.txt.
var diacritics = []rune{
	0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F, 0x0346, 0x034A, 0x034B, 0x034C,
	0x0350, 0x0351, 0x0352, 0x0357, 0x035B, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369,
	0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F, 0x0483, 0x0484, 0x0485, 0x0486, 0x0487, 0x0592,
	0x0593, 0x0594, 0x0595, 0x0597, 0x0598, 0x0599, 0x059C, 0x059D, 0x059E, 0x059F, 0x05A0, 0x05A1,
	0x05A8, 0x05A9, 0x05AB, 0x05AC, 0x05AF, 0x05C4, 0x0610, 0x0611, 0x0612, 0x0613, 0x0614, 0x0615,
	0x0616, 0x0617, 0x0657, 0x0658, 0x0659, 0x065A, 0x065B, 0x065D, 0x065E, 0x06D6, 0x06D7, 0x06D8,
	0x06D9, 0x06DA, 0x06DB, 0x06DC, 0x06DF, 0x06E0, 0x06E1, 0x06E2, 0x06E4, 0x06E7, 0x06E8, 0x06EB,
	0x06EC, 0x0730, 0x0732, 0x0733, 0x0735, 0x0736, 0x073A, 0x073D, 0x073F, 0x0740, 0x0741, 0x0743,
	0x0745, 0x0747, 0x0749, 0x074A, 0x07EB, 0x07EC, 0x07ED, 0x07EE, 0x07EF, 0x07F0, 0x07F1, 0x07F3,
	0x0816, 0x0817, 0x0818, 0x0819, 0x081B, 0x081C, 0x081D, 0x081E, 0x081F, 0x0820, 0x0821, 0x0822,
	0x0823, 0x0825, 0x0826, 0x0827, 0x0829, 0x082A, 0x082B, 0x082C, 0x082D, 0x0951, 0x0953, 0x0954,
	0x0F82, 0x0F83, 0x0F86, 0x0F87, 0x135D, 0x135E, 0x135F, 0x17DD, 0x193A, 0x1A17, 0x1A75, 0x1A76,
	0x1A77, 0x1A78, 0x1A79, 0x1A7A, 0x1A7B, 0x1A7C, 0x1B6B, 0x1B6D, 0x1B6E, 0x1B6F, 0x1B70, 0x1B71,
	0x1B72, 0x1B73, 0x1CD0, 0x1CD1, 0x1CD2, 0x1CDA, 0x1CDB, 0x1CE0, 0x1DC0, 0x1DC1, 0x1DC3, 0x1DC4,
	0x1DC5, 0x1DC6, 0x1DC7, 0x1DC8, 0x1DC9, 0x1DCB, 0x1DCC, 0x1DD1, 0x1DD2, 0x1DD3, 0x1DD4, 0x1DD5,
	0x1DD6, 0x1DD7, 0x1DD8, 0x1DD9, 0x1DDA, 0x1DDB, 0x1DDC, 0x1DDD, 0x1DDE, 0x1DDF, 0x1DE0, 0x1DE1,
	0x1DE2, 0x1DE3, 0x1DE4, 0x1DE5, 0x1DE6, 0x1DFE, 0x20D0, 0x20D1, 0x20D4, 0x20D5, 0x20D6, 0x20D7,
	0x20DB, 0x20DC, 0x20E1, 0x20E7, 0x20E9, 0x20F0, 0x2CEF, 0x2CF0, 0x2CF1, 0x2DE0, 0x2DE1, 0x2DE2,
	0x2DE3, 0x2DE4, 0x2DE5, 0x2DE6, 0x2DE7, 0x2DE8, 0x2DE9, 0x2DEA, 0x2DEB, 0x2DEC, 0x2DED, 0x2DEE,
	0x2DEF, 0x2DF0, 0x2DF1, 0x2DF2, 0x2DF3, 0x2DF4, 0x2DF5, 0x2DF6, 0x2DF7, 0x2DF8, 0x2DF9, 0x2DFA,
	0x2DFB, 0x2DFC, 0x2DFD, 0x2DFE, 0x2DFF, 0xA66F, 0xA67C, 0xA67D, 0xA6F0, 0xA6F1, 0xA8E0, 0xA8E1,
	0xA8E2, 0xA8E3, 0xA8E4, 0xA8E5, 0xA8E6, 0xA8E7, 0xA8E8, 0xA8E9, 0xA8EA, 0xA8EB, 0xA8EC, 0xA8ED,
	0xA8EE, 0xA8EF, 0xA8F0, 0xA8F1, 0xAAB0, 0xAAB2, 0xAAB3, 0xAAB7, 0xAAB8, 0xAABE, 0xAABF, 0xAAC1,
	0xFE20, 0xFE21, 0xFE22, 0xFE23, 0xFE24, 0xFE25, 0xFE26, 0x10A0F, 0x10A38, 0x1D185, 0x1D186, 0x1D187,
	0x1D188, 0x1D189, 0x1D1AA, 0x1D1AB, 0x1D1AC, 0x1D1AD, 0x1D242, 0x1D243, 0x1D244,
}

func diacritic(n int) rune {
	if n < 0 {
		n = 0
	}
	if n >= len(diacritics) {
		n = len(diacritics) - 1
	}
	return diacritics[n]
}

// Graphics is the pixel decision and the size of one cell in pixels.
type Graphics struct {
	Pixel                 bool
	CellWidth, CellHeight int
}

// Probe is the answer to a terminal query. CellWidth and CellHeight may be
// filled from TIOCGWINSZ; Detect also reads them from a CSI 16 t reply.
type Probe struct {
	Reply                 string
	CellWidth, CellHeight int
}

// QueryFunc writes a request to the terminal and returns what it answered
// within timeout. A test supplies its own function.
type QueryFunc func(request string, timeout time.Duration) Probe

// Detect chooses the pixel renderer or Braille. AGENT_NET_GRAPH is
// braille, pixel or auto (the default). Braille is chosen when the switch
// says so, when a multiplexer that drops images is present (TMUX, or herdr's
// HERDR_ENV=1), when the terminal does not answer the kitty probe with OK, or
// when the cell size in pixels is unknown.
func Detect(getenv func(string) string, query QueryFunc) Graphics {
	mode := strings.ToLower(strings.TrimSpace(getenv("AGENT_NET_GRAPH")))
	if mode == "braille" {
		return Graphics{}
	}
	mux := getenv("TMUX") != "" || getenv("HERDR_ENV") == "1"
	if mux && mode != "pixel" {
		return Graphics{}
	}
	probe := Probe{}
	if query != nil {
		probe = query(kittyProbe+cellProbe+deviceAttributes, 250*time.Millisecond)
	}
	width, height := probe.CellWidth, probe.CellHeight
	if width <= 0 || height <= 0 {
		width, height = cellPixels(probe.Reply)
	}
	if width <= 0 || height <= 0 {
		return Graphics{}
	}
	if mode == "pixel" {
		return Graphics{Pixel: true, CellWidth: width, CellHeight: height}
	}
	if mux {
		return Graphics{}
	}
	if !strings.Contains(probe.Reply, "\x1b_G") || !strings.Contains(probe.Reply, ";OK") {
		return Graphics{}
	}
	return Graphics{Pixel: true, CellWidth: width, CellHeight: height}
}

var cellSizeReply = regexp.MustCompile(`\x1b\[6;(\d+);(\d+)t`)

func cellPixels(reply string) (int, int) {
	m := cellSizeReply.FindStringSubmatch(reply)
	if m == nil {
		return 0, 0
	}
	height, width := atoi(m[1]), atoi(m[2])
	return width, height
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

const (
	kittyProbe       = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	cellProbe        = "\x1b[16t"
	deviceAttributes = "\x1b[c"
)

var deviceAttributesReply = regexp.MustCompile(`\x1b\[[?>]?[0-9;]*c`)

// QueryTerminal asks the real terminal. It switches stdin to raw mode for the
// short wait, writes the request and returns everything the terminal answered
// before the device attributes reply or the timeout.
func QueryTerminal(request string, timeout time.Duration) Probe {
	p := Probe{}
	if ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ); err == nil && ws.Col > 0 && ws.Row > 0 && ws.Xpixel > 0 && ws.Ypixel > 0 {
		p.CellWidth, p.CellHeight = int(ws.Xpixel)/int(ws.Col), int(ws.Ypixel)/int(ws.Row)
	}
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return p
	}
	defer term.Restore(fd, old) //nolint:errcheck
	_, _ = os.Stdout.WriteString(request)
	deadline := time.Now().Add(timeout)
	var b strings.Builder
	buf := make([]byte, 256)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, int(left.Milliseconds()))
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			break
		}
		if n == 0 {
			break
		}
		m, err := os.Stdin.Read(buf)
		if m > 0 {
			b.Write(buf[:m])
		}
		if err != nil {
			break
		}
		if deviceAttributesReply.MatchString(b.String()) {
			break
		}
	}
	p.Reply = b.String()
	return p
}

// TransmitImage returns the kitty escape codes that send img and create its
// virtual placement over cols by rows cells. The payload is chunked to the
// protocol's 4096-byte limit.
func TransmitImage(img *image.RGBA, id uint32, cols, rows int) string {
	if img == nil || cols <= 0 || rows <= 0 {
		return ""
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return ""
	}
	data := base64.StdEncoding.EncodeToString(pngBuf.Bytes())
	var b strings.Builder
	first := true
	for len(data) > 0 {
		n := 4096
		if len(data) < n {
			n = len(data)
		}
		more := 0
		if len(data) > n {
			more = 1
		}
		if first {
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,U=1,i=%d,q=2,c=%d,r=%d,m=%d;", id, cols, rows, more)
			first = false
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;", more)
		}
		b.WriteString(data[:n])
		b.WriteString("\x1b\\")
		data = data[n:]
	}
	return b.String()
}

// DeleteImage frees an image and all of its placements.
func DeleteImage(id uint32) string {
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
}

// PlaceholderLines turns the image area into terminal rows of U+10EEEE
// placeholder cells. Label cells stay ordinary text, so they remain crisp and
// selectable, and they keep the style of the Braille renderer.
func PlaceholderLines(labels map[Cell]LabelCell, size Size, id uint32, style func(role, text string) string) []string {
	if size.Width <= 0 || size.Height <= 0 {
		return nil
	}
	colour := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", (id>>16)&0xff, (id>>8)&0xff, id&0xff)
	reset := "\x1b[0m"
	lines := make([]string, size.Height)
	for y := 0; y < size.Height; y++ {
		var b strings.Builder
		coloured := false
		for x := 0; x < size.Width; x++ {
			if l, ok := labels[Cell{X: x, Y: y}]; ok && l.Rune != 0 {
				if coloured {
					b.WriteString(reset)
					coloured = false
				}
				if style != nil {
					b.WriteString(style(l.Role, string(l.Rune)))
				} else {
					b.WriteRune(l.Rune)
				}
				continue
			}
			if !coloured {
				b.WriteString(colour)
				coloured = true
			}
			b.WriteRune(placeholder)
			b.WriteRune(diacritic(y))
			b.WriteRune(diacritic(x))
		}
		if coloured {
			b.WriteString(reset)
		}
		lines[y] = b.String()
	}
	return lines
}
