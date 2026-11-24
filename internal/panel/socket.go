package panel

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
)

type socketTracker struct {
	http.ResponseWriter
	onHijack func(net.Conn)
}

func (w *socketTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijack unavailable")
	}
	c, b, e := h.Hijack()
	if e == nil {
		w.onHijack(c)
	}
	return c, b, e
}
func (w *socketTracker) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
