package panel

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"
)

type directorySize struct {
	Bytes          *uint64 `json:"bytes"`
	AllocatedBytes uint64  `json:"allocatedBytes,omitempty"`
	Files          uint64  `json:"files,omitempty"`
	State          string  `json:"state"`
	Complete       bool    `json:"complete"`
	UpdatedAt      int64   `json:"updatedAt,omitempty"`
}
type fileSearchSize struct {
	Path           string  `json:"path"`
	LogicalBytes   *uint64 `json:"logical_bytes"`
	AllocatedBytes uint64  `json:"allocated_bytes"`
	Files          uint64  `json:"files"`
	State          string  `json:"state"`
	Complete       bool    `json:"complete"`
	UpdatedAt      int64   `json:"updated_at_ms"`
}

func fileSearchSocket() string {
	if socket := os.Getenv("OWNPANEL_FILESEARCH_SOCKET"); socket != "" {
		return socket
	}
	return filepath.Join(cfg.data, "filesearch", "search.sock")
}

// Local framed IPC only; no subprocess, filesystem walk, or service start on reads.
func queryFileSearchSizes(ctx context.Context, socket string, paths []string) ([]fileSearchSize, error) {
	if len(paths) > 128 {
		return nil, fmt.Errorf("too many directory paths")
	}
	var result struct {
		Entries []fileSearchSize `json:"entries"`
	}
	err := queryFileSearch(ctx, socket, map[string]any{"command": "directory_sizes", "paths": paths}, &result)
	return result.Entries, err
}
func queryFileSearch(ctx context.Context, socket string, request any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer connection.Close()
	deadline, _ := ctx.Deadline()
	_ = connection.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(payload) > 1024*1024 {
		return fmt.Errorf("directory query too large")
	}
	frame := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint32(frame, uint32(len(payload)))
	copy(frame[4:], payload)
	for len(frame) > 0 {
		n, e := connection.Write(frame)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	var header [4]byte
	if _, err = io.ReadFull(connection, header[:]); err != nil {
		return err
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size == 0 || size > 1024*1024 {
		return fmt.Errorf("invalid FileSearch frame size")
	}
	payload = make([]byte, size)
	if _, err = io.ReadFull(connection, payload); err != nil {
		return err
	}
	var envelope struct {
		OK bool `json:"ok"`
	}
	if err = json.Unmarshal(payload, &envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return fmt.Errorf("FileSearch is not ready")
	}
	return json.Unmarshal(payload, result)
}
func handleDirectorySizes(w http.ResponseWriter, r *http.Request) {
	p, err := relative(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	root, err := fileRoot()
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer root.Close()
	directory, err := root.Open(p)
	if err != nil {
		fail(w, 400, err)
		return
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		fail(w, 400, err)
		return
	}
	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	cursor := r.URL.Query().Get("cursor")
	first := sort.SearchStrings(names, cursor)
	for first < len(names) && names[first] <= cursor {
		first++
	}
	end := first + 128
	if end > len(names) {
		end = len(names)
	}
	result := map[string]directorySize{}
	paths := []string{}
	requested := map[string]string{}
	for _, name := range names[first:end] {
		absolute := filepath.Join(cfg.fileRoot, filepath.FromSlash(path.Join(p, name)))
		paths = append(paths, absolute)
		requested[absolute] = name
	}
	next := ""
	if end < len(names) && end > first {
		next = names[end-1]
	}
	if len(paths) == 0 {
		respond(w, map[string]any{"available": true, "entries": result, "next": ""})
		return
	}
	sizes, err := queryFileSearchSizes(r.Context(), fileSearchSocket(), paths)
	if err != nil {
		respond(w, map[string]any{"available": false, "entries": result, "next": ""})
		return
	}
	for _, entry := range sizes {
		name, ok := requested[entry.Path]
		if !ok {
			continue
		}
		switch entry.State {
		case "ready", "updating", "stale", "partial", "calculating", "excluded", "unavailable":
		default:
			entry.State = "unavailable"
			entry.LogicalBytes = nil
		}
		result[name] = directorySize{Bytes: entry.LogicalBytes, AllocatedBytes: entry.AllocatedBytes, Files: entry.Files, State: entry.State, Complete: entry.Complete, UpdatedAt: entry.UpdatedAt}
	}
	respond(w, map[string]any{"available": true, "entries": result, "next": next})
}
