package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

type panelUserKey struct{}

func panelUser(ctx context.Context) int64 {
	id, _ := ctx.Value(panelUserKey{}).(int64)
	return id
}

// The process belongs to its driver, never to the browser request. Detached
// sessions keep draining output into a bounded replay buffer.
type terminalSession struct {
	owner   int64
	mu      sync.Mutex
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Ended   bool      `json:"ended"`
	Cols    uint16    `json:"cols"`
	Rows    uint16    `json:"rows"`
	stdin   io.WriteCloser
	resize  func(uint16, uint16) error
	stop    func()
	output  []byte
	peers   map[*websocket.Conn]chan []byte
}
type terminalHub struct {
	mu       sync.Mutex
	sessions map[string]*terminalSession
	start    func(context.Context, *terminalSession) error
	closed   bool
}

func (h *terminalHub) close() {
	h.mu.Lock()
	h.closed = true
	sessions := h.sessions
	h.sessions = nil
	h.mu.Unlock()
	for _, s := range sessions {
		s.terminate()
	}
}
func (s *terminalSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const limit = 2 << 20
	if len(p) >= limit {
		s.output = append(s.output[:0], p[len(p)-limit:]...)
	} else {
		if len(s.output)+len(p) > limit {
			s.output = append([]byte(nil), s.output[len(s.output)+len(p)-limit:]...)
		}
		s.output = append(s.output, p...)
	}
	for conn, q := range s.peers {
		select {
		case q <- append([]byte(nil), p...):
		default:
			conn.Close()
			delete(s.peers, conn)
		}
	}
	return len(p), nil
}
func (s *terminalSession) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Ended = true
	for c := range s.peers {
		c.Close()
		delete(s.peers, c)
	}
}
func (s *terminalSession) terminate() {
	if s.stop != nil {
		s.stop()
	}
	s.finish()
}
func (s *terminalSession) info() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"id": s.ID, "name": s.Name, "created": s.Created, "ended": s.Ended, "cols": s.Cols, "rows": s.Rows, "viewers": len(s.peers)}
}
func (h *terminalHub) create(ctx context.Context, name string) (*terminalSession, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, fmt.Errorf("主机连接已关闭")
	}
	if h.sessions == nil {
		h.sessions = map[string]*terminalSession{}
	}
	if len(h.sessions) >= 32 {
		return nil, fmt.Errorf("已有 32 个终端，请结束不需要的会话")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "终端 " + time.Now().Format("15:04:05")
	}
	if len(name) > 120 {
		return nil, fmt.Errorf("终端名称过长")
	}
	s := &terminalSession{owner: panelUser(ctx), ID: token()[:32], Name: name, Created: time.Now(), Cols: 100, Rows: 30, peers: map[*websocket.Conn]chan []byte{}}
	if err := h.start(ctx, s); err != nil {
		return nil, err
	}
	h.sessions[s.ID] = s
	return s, nil
}
func (h *terminalHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	owner := panelUser(r.Context())
	if r.URL.Path == "/api/terminals" {
		switch r.Method {
		case "GET":
			h.mu.Lock()
			items := []map[string]any{}
			for _, s := range h.sessions {
				if s.owner == owner {
					items = append(items, s.info())
				}
			}
			h.mu.Unlock()
			sort.Slice(items, func(i, j int) bool { return items[i]["created"].(time.Time).Before(items[j]["created"].(time.Time)) })
			respond(w, map[string]any{"sessions": items})
		case "POST":
			var b struct {
				Name string `json:"name"`
			}
			if e := decode(w, r, &b); e != nil {
				fail(w, 400, e)
				return
			}
			s, e := h.create(r.Context(), b.Name)
			if e != nil {
				fail(w, 400, e)
				return
			}
			respond(w, s.info())
		default:
			w.WriteHeader(405)
		}
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/terminals/") && r.Method == "PATCH" {
		var b struct {
			Name string `json:"name"`
		}
		if e := decode(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		b.Name = strings.TrimSpace(b.Name)
		if b.Name == "" || utf8.RuneCountInString(b.Name) > 60 {
			fail(w, 400, fmt.Errorf("请输入 1–60 个字符"))
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/terminals/")
		h.mu.Lock()
		s := h.sessions[id]
		if s != nil && s.owner != owner {
			s = nil
		}
		if s != nil {
			s.mu.Lock()
			s.Name = b.Name
			s.mu.Unlock()
		}
		h.mu.Unlock()
		if s == nil {
			fail(w, 404, fmt.Errorf("终端不存在"))
			return
		}
		respond(w, s.info())
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/terminals/") && r.Method == "DELETE" {
		id := strings.TrimPrefix(r.URL.Path, "/api/terminals/")
		h.mu.Lock()
		s := h.sessions[id]
		if s != nil && s.owner != owner {
			s = nil
		}
		if s != nil {
			delete(h.sessions, id)
		}
		h.mu.Unlock()
		if s == nil {
			fail(w, 404, fmt.Errorf("终端不存在"))
			return
		}
		s.terminate()
		respond(w, map[string]bool{"ok": true})
		return
	}
	if r.URL.Path != "/api/terminal" || r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	id := r.URL.Query().Get("session")
	ephemeral := id == ""
	h.mu.Lock()
	s := h.sessions[id]
	if s != nil && s.owner != owner {
		s = nil
	}
	h.mu.Unlock()
	if ephemeral {
		var e error
		s, e = h.create(r.Context(), "")
		if e != nil {
			fail(w, 400, e)
			return
		}
		defer func() { h.mu.Lock(); delete(h.sessions, s.ID); h.mu.Unlock(); s.terminate() }()
	}
	if s == nil {
		fail(w, 404, fmt.Errorf("终端不存在，请刷新会话列表"))
		return
	}
	conn, e := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
	if e != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(128 << 10)
	queue := make(chan []byte, 128)
	s.mu.Lock()
	replay := append([]byte(nil), s.output...)
	ended := s.Ended
	if !ended {
		s.peers[conn] = queue
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.peers, conn); s.mu.Unlock() }()
	if len(replay) > 0 {
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if conn.WriteMessage(websocket.BinaryMessage, replay) != nil {
			return
		}
	}
	if ended {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "进程已结束"), time.Now().Add(time.Second))
		return
	}
	stop := make(chan struct{})
	defer close(stop)
	conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(70 * time.Second)) })
	go func() {
		tick := time.NewTicker(20 * time.Second)
		defer tick.Stop()
		for {
			select {
			case p := <-queue:
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if conn.WriteMessage(websocket.BinaryMessage, p) != nil {
					conn.Close()
					return
				}
			case <-tick.C:
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					conn.Close()
					return
				}
			case <-stop:
				return
			}
		}
	}()
	for {
		_, p, e := conn.ReadMessage()
		if e != nil {
			return
		}
		var m struct {
			Type, Data string
			Cols, Rows uint16
		}
		if json.Unmarshal(p, &m) != nil {
			continue
		}
		switch m.Type {
		case "input":
			if _, e = io.WriteString(s.stdin, m.Data); e != nil {
				return
			}
		case "resize":
			if m.Cols > 0 && m.Cols <= 500 && m.Rows > 0 && m.Rows <= 300 {
				s.mu.Lock()
				s.Cols = m.Cols
				s.Rows = m.Rows
				s.mu.Unlock()
				_ = s.resize(m.Cols, m.Rows)
			}
		}
	}
}
