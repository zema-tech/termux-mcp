// Package server – terminal streaming handler: tails a background task's
// stdout.log and streams it to the client as Server-Sent Events.
package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"termux-mcp/internal/tasks"
)

var taskIDRe = regexp.MustCompile(`^t[0-9]{6}$`)

// TerminalStreamHandler returns an http.Handler for GET /terminal/stream.
// Query params:
//
//	task_id  – required, e.g. "t000001"
//	stream   – "stdout" (default) or "all"
func TerminalStreamHandler(mgr *tasks.Manager, taskDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		taskID := r.URL.Query().Get("task_id")
		if taskID == "" {
			http.Error(w, `{"error":"missing task_id"}`, http.StatusBadRequest)
			return
		}
		if !taskIDRe.MatchString(taskID) {
			http.Error(w, `{"error":"invalid task_id"}`, http.StatusBadRequest)
			return
		}

		sse, ok := newSSEWriter(w)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		info, err := mgr.Status(taskID)
		if err != nil {
			_ = sse.send("error", `{"message":"task not found"}`)
			return
		}

		streamParam := r.URL.Query().Get("stream")
		type logPath struct{ event, path string }
		paths := []logPath{{"output", filepath.Join(taskDir, taskID, "stdout.log")}}
		if streamParam == "all" {
			paths = append(paths, logPath{"stderr", filepath.Join(taskDir, taskID, "stderr.log")})
		}
		type openStream struct {
			event string
			file  *os.File
			seen  int64
		}
		var streams []openStream
		for _, p := range paths {
			f, err := os.Open(p.path)
			if err != nil {
				continue // log may not exist yet
			}
			defer f.Close()
			streams = append(streams, openStream{event: p.event, file: f})
		}
		if len(streams) == 0 {
			_ = sse.send("error", `{"message":"log file not available"}`)
			return
		}

		// Send initial task info.
		initMsg, _ := json.Marshal(map[string]any{
			"task_id": info.ID,
			"command": info.Command,
			"state":   info.State,
			"pid":     info.PID,
		})
		_ = sse.send("init", string(initMsg))

		// If the task already finished, send whatever is in the log and exit.
		if info.State != tasks.StateRunning {
			for _, st := range streams {
				if data, err := io.ReadAll(st.file); err == nil && len(data) > 0 {
					_ = sse.sendBytes(st.event, data)
				}
			}
			finalMsg, _ := json.Marshal(map[string]any{
				"type":      "done",
				"exit_code": info.ExitCode,
				"state":     info.State,
			})
			_ = sse.send("done", string(finalMsg))
			return
		}

		// Tail the log file until the task finishes.
		pollInterval := 100 * time.Millisecond
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				// Read any new bytes from every stream.
				for i := range streams {
					st := &streams[i]
					stat, err := st.file.Stat()
					if err != nil {
						continue
					}
					size := stat.Size()
					if size > st.seen {
						buf := make([]byte, size-st.seen)
						n, err := st.file.ReadAt(buf, st.seen)
						if n > 0 {
							st.seen += int64(n)
							_ = sse.sendBytes(st.event, buf[:n])
						}
						if err != nil && err != io.EOF {
							slog.Warn("terminal stream read error", "err", err)
						}
					}
				}

				// Check if the task finished.
				info, err = mgr.Status(taskID)
				if err != nil {
					return
				}
				if info.State != tasks.StateRunning {
					// Drain any remaining bytes.
					for i := range streams {
						st := &streams[i]
						if stat, err := st.file.Stat(); err == nil && stat.Size() > st.seen {
							buf := make([]byte, stat.Size()-st.seen)
							n, _ := st.file.ReadAt(buf, st.seen)
							if n > 0 {
								_ = sse.sendBytes(st.event, buf[:n])
							}
						}
					}
					finalMsg, _ := json.Marshal(map[string]any{
						"type":      "done",
						"exit_code": info.ExitCode,
						"state":     info.State,
						"duration":  info.DurationMS,
					})
					_ = sse.send("done", string(finalMsg))
					return
				}
			}
		}
	})
}
