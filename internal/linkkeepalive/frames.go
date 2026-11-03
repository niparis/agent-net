package linkkeepalive

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"net/http"
)

// frameObserver skips payload bytes and reports each complete WebSocket frame.
// The WebSocket library validates frames; this only tracks their boundaries.
type frameObserver struct {
	received  func()
	header    [14]byte
	have      int
	need      int
	remaining uint64
}

func (f *frameObserver) observe(p []byte) {
	for len(p) > 0 {
		if f.remaining > 0 {
			n := min(uint64(len(p)), f.remaining)
			f.remaining -= n
			p = p[int(n):]
			if f.remaining == 0 {
				f.received()
			}
			continue
		}
		if f.need == 0 {
			f.need = 2
		}
		n := copy(f.header[f.have:f.need], p)
		f.have += n
		p = p[n:]
		if f.have < f.need {
			continue
		}
		if f.need == 2 {
			length := f.header[1] & 0x7f
			if length == 126 {
				f.need += 2
			} else if length == 127 {
				f.need += 8
			}
			if f.header[1]&0x80 != 0 {
				f.need += 4
			}
			if f.have < f.need {
				continue
			}
		}
		switch length := f.header[1] & 0x7f; length {
		case 126:
			f.remaining = uint64(binary.BigEndian.Uint16(f.header[2:4]))
		case 127:
			f.remaining = binary.BigEndian.Uint64(f.header[2:10])
		default:
			f.remaining = uint64(length)
		}
		f.have, f.need = 0, 0
		if f.remaining == 0 {
			f.received()
		}
	}
}

type trackedBody struct {
	io.ReadWriteCloser
	frames frameObserver
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.ReadWriteCloser.Read(p)
	b.frames.observe(p[:n])
	return n, err
}

type trackedTransport struct {
	base   http.RoundTripper
	health *Monitor
}

func (t trackedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err == nil && resp.StatusCode == http.StatusSwitchingProtocols {
		if body, ok := resp.Body.(io.ReadWriteCloser); ok {
			resp.Body = &trackedBody{ReadWriteCloser: body, frames: frameObserver{received: t.health.Received}}
		}
	}
	return resp, err
}

// Client wraps only the upgraded WebSocket response, leaving the supplied
// client's transport, TLS settings and other requests unchanged.
func (m *Monitor) Client(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copy.Transport = trackedTransport{base: base, health: m}
	return &copy
}

type trackedConn struct {
	net.Conn
	frames *frameObserver
}

func (c *trackedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.frames.observe(p[:n])
	return n, err
}

type trackedResponseWriter struct {
	http.ResponseWriter
	health *Monitor
}

func (w trackedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	frames := &frameObserver{received: w.health.Received}
	// The HTTP server may already have read some WebSocket bytes with the
	// request. Accept preserves these bytes in its buffered reader.
	buffered, _ := rw.Reader.Peek(rw.Reader.Buffered())
	frames.observe(buffered)
	return &trackedConn{Conn: conn, frames: frames}, rw, nil
}

// ResponseWriter observes bytes after the gateway upgrades a node connection.
func (m *Monitor) ResponseWriter(w http.ResponseWriter) http.ResponseWriter {
	return trackedResponseWriter{ResponseWriter: w, health: m}
}
