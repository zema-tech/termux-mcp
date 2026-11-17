// Package files implements Module E: filesystem tools (sensitive tier) with
// a path sandbox restricting access to configured roots.
package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"termux-mcp/internal/config"
	"termux-mcp/internal/registry"
	"termux-mcp/internal/tools/kit"
)

// All returns the files module's tools.
func All(k *kit.Kit) []registry.Tool {
	return []registry.Tool{
		{Def: mcp.NewTool("list_directory",
			mcp.WithDescription("List a directory inside an allowed file root."),
			mcp.WithString("path", mcp.Required(), mcp.Description("Directory path"))),
			Handler: listDir(k),
			Meta:    registry.Meta{Module: "files", Tier: registry.TierSensitive}},

		{Def: mcp.NewTool("read_file",
			mcp.WithDescription("Read a text file inside an allowed file root."),
			mcp.WithString("path", mcp.Required(), mcp.Description("File path")),
			mcp.WithNumber("max_chars", mcp.Description("Max characters to return (default 100000)"), mcp.Min(1))),
			Handler: readFile(k),
			Meta:    registry.Meta{Module: "files", Tier: registry.TierSensitive}},

		{Def: mcp.NewTool("write_file",
			mcp.WithDescription("Write text to a file inside an allowed file root."),
			mcp.WithString("path", mcp.Required(), mcp.Description("File path")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Content to write"))),
			Handler: writeFile(k),
			Meta:    registry.Meta{Module: "files", Tier: registry.TierSensitive}},

		{Def: mcp.NewTool("download_file",
			mcp.WithDescription("Download a URL to a file inside an allowed root via termux-download."),
			mcp.WithString("url", mcp.Required(), mcp.Description("URL to download")),
			mcp.WithString("output", mcp.Required(), mcp.Description("Output file path (inside an allowed root)")),
			mcp.WithString("title", mcp.Description("Download title")),
			mcp.WithString("description", mcp.Description("Download description"))),
			Handler: downloadFile(k),
			Meta:    registry.Meta{Module: "files", Tier: registry.TierSensitive}},

		{Def: mcp.NewTool("share_file",
			mcp.WithDescription("Open the Android share sheet for a file via termux-share."),
			mcp.WithString("path", mcp.Required(), mcp.Description("File path"))),
			Handler: shareFile(k),
			Meta:    registry.Meta{Module: "files", Tier: registry.TierSensitive}},

		{Def: mcp.NewTool("media_scan",
			mcp.WithDescription("Scan a file or directory so it appears in the media library via termux-media-scan."),
			mcp.WithString("path", mcp.Required(), mcp.Description("File or directory path"))),
			Handler: mediaScan(k),
			Meta:    registry.Meta{Module: "files", Tier: registry.TierSensitive}},

		{Def: mcp.NewTool("search_files",
			mcp.WithDescription("Search for files by name pattern inside an allowed file root."),
			mcp.WithString("path", mcp.Required(), mcp.Description("Directory to search")),
			mcp.WithString("pattern", mcp.Description("Glob pattern for file names (default *)")),
			mcp.WithNumber("max_results", mcp.Description("Max results to return (default 200, cap 1000)"), mcp.Min(1)),
			mcp.WithBoolean("recursive", mcp.Description("Recurse into subdirectories (default true)"))),
			Handler: searchFiles(k),
			Meta:    registry.Meta{Module: "files", Tier: registry.TierSensitive}},

		{Def: mcp.NewTool("file_info",
			mcp.WithDescription("Get metadata for a file or directory inside an allowed file root."),
			mcp.WithString("path", mcp.Required(), mcp.Description("File or directory path"))),
			Handler: fileInfo(k),
			Meta:    registry.Meta{Module: "files", Tier: registry.TierSensitive}},

		{Def: mcp.NewTool("move_file",
			mcp.WithDescription("Move or rename a file inside allowed roots. Falls back to copy+remove across devices."),
			mcp.WithString("source", mcp.Required(), mcp.Description("Source path")),
			mcp.WithString("destination", mcp.Required(), mcp.Description("Destination path"))),
			Handler: moveFile(k),
			Meta:    registry.Meta{Module: "files", Tier: registry.TierDangerous}},

		{Def: mcp.NewTool("delete_file",
			mcp.WithDescription("Delete a file or directory inside an allowed root. Refuses to delete sandbox roots."),
			mcp.WithString("path", mcp.Required(), mcp.Description("Path to delete")),
			mcp.WithBoolean("recursive", mcp.Description("Delete directories recursively (default false)"))),
			Handler: deleteFile(k),
			Meta:    registry.Meta{Module: "files", Tier: registry.TierDangerous}},
	}
}

// Roots returns the configured sandbox roots, defaulting to the home dir
// and /sdcard when none are configured.
func Roots(tc *config.ToolsConfig) []string {
	if len(tc.FileRoots) > 0 {
		return tc.FileRoots
	}
	var roots []string
	if h, err := os.UserHomeDir(); err == nil {
		roots = append(roots, h)
	}
	roots = append(roots, "/sdcard")
	return roots
}

// resolveExistingAncestor walks up from p until it finds an existing path,
// EvalSymlinks that ancestor, then re-appends the missing rest.
func resolveExistingAncestor(p string) string {
	rest := make([]string, 0, 4)
	cur := p
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		}
		dir, base := filepath.Dir(cur), filepath.Base(cur)
		if dir == cur {
			break
		}
		rest = append(rest, base)
		cur = dir
	}
	if resolved, err := filepath.EvalSymlinks(cur); err == nil {
		cur = resolved
	}
	for i := len(rest) - 1; i >= 0; i-- {
		cur = filepath.Join(cur, rest[i])
	}
	return filepath.Clean(cur)
}

func resolveRoot(r string) string {
	rr := filepath.Clean(r)
	if !filepath.IsAbs(rr) {
		if abs, err := filepath.Abs(rr); err == nil {
			rr = abs
		}
	}
	if resolved, err := filepath.EvalSymlinks(rr); err == nil {
		rr = resolved
	}
	return filepath.Clean(rr)
}

// Resolve cleans raw, makes it absolute, resolves existing symlinks along the
// path, and verifies the result stays inside one of the allowed roots.
func Resolve(raw string, roots []string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("empty path")
	}
	p, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", err
	}
	p = filepath.Clean(resolveExistingAncestor(p))
	for _, r := range roots {
		rr := resolveRoot(r)
		if rr == p || strings.HasPrefix(p, rr+string(filepath.Separator)) {
			return p, nil
		}
	}
	return "", fmt.Errorf("path %q is outside allowed roots %v", raw, roots)
}

type dirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

func listDir(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, err := kit.RequireStr(req, "path")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		p, err := Resolve(raw, Roots(&k.Cfg.Tools))
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return kit.ResultError("read dir: %v", err), nil
		}
		out := make([]dirEntry, 0, len(entries))
		for _, e := range entries {
			info, ierr := e.Info()
			sz := int64(0)
			if ierr == nil {
				sz = info.Size()
			}
			out = append(out, dirEntry{Name: e.Name(), Path: filepath.Join(p, e.Name()), IsDir: e.IsDir(), Size: sz})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return kit.ResultJSON(map[string]any{"path": p, "count": len(out), "entries": out}), nil
	}
}

func readFile(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, err := kit.RequireStr(req, "path")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		p, err := Resolve(raw, Roots(&k.Cfg.Tools))
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return kit.ResultError("read file: %v", err), nil
		}
		max := int64(100000)
		if m := int64(math.Round(kit.NumArg(req, "max_chars"))); m > 0 {
			max = m
		}
		truncated := false
		if int64(len(data)) > max {
			data = data[:max]
			truncated = true
		}
		return kit.ResultJSON(map[string]any{
			"path":      p,
			"size":      len(data),
			"truncated": truncated,
			"content":   string(data),
		}), nil
	}
}

func writeFile(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, err := kit.RequireStr(req, "path")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		content, err := kit.RequireStr(req, "content")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		p, err := Resolve(raw, Roots(&k.Cfg.Tools))
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		if int64(len(content)) > k.Cfg.Exec.MaxOutputBytes {
			return kit.ResultError("content too large (%d bytes, cap %d)", len(content), k.Cfg.Exec.MaxOutputBytes), nil
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return kit.ResultError("write file: %v", err), nil
		}
		return kit.ResultJSON(map[string]any{"path": p, "bytes_written": len(content)}), nil
	}
}

func downloadFile(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		url, err := kit.RequireStr(req, "url")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		out, err := kit.RequireStr(req, "output")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		p, err := Resolve(out, Roots(&k.Cfg.Tools))
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		args := []string{"-o", p}
		if t := kit.StrArg(req, "title"); t != "" {
			args = append(args, "-t", t)
		}
		if d := kit.StrArg(req, "description"); d != "" {
			args = append(args, "-d", d)
		}
		args = append(args, url)
		res, err := k.Run(ctx, "termux-download", args, 0)
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		if res.ExitCode != 0 {
			return kit.ResultError("termux-download exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), nil
		}
		return kit.ResultText("downloaded to %s", p), nil
	}
}

func shareFile(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, err := kit.RequireStr(req, "path")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		p, err := Resolve(raw, Roots(&k.Cfg.Tools))
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		res, err := k.Run(ctx, "termux-share", []string{p}, 0)
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		if res.ExitCode != 0 {
			return kit.ResultError("termux-share exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), nil
		}
		return kit.ResultText("share sheet opened for %s", p), nil
	}
}

func mediaScan(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, err := kit.RequireStr(req, "path")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		p, err := Resolve(raw, Roots(&k.Cfg.Tools))
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		res, err := k.Run(ctx, "termux-media-scan", []string{p}, 0)
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		if res.ExitCode != 0 {
			return kit.ResultError("termux-media-scan exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), nil
		}
		return kit.ResultText("media scan queued for %s", p), nil
	}
}

type searchEntry struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

func searchFiles(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, err := kit.RequireStr(req, "path")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		p, err := Resolve(raw, Roots(&k.Cfg.Tools))
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		pattern := kit.StrArg(req, "pattern")
		if pattern == "" {
			pattern = "*"
		}
		if _, err := filepath.Match(pattern, "x"); err != nil {
			return kit.ResultError("invalid pattern %q: %v", pattern, err), nil
		}
		max := 200
		if m := kit.NumArg(req, "max_results"); m > 0 {
			max = int(m)
		}
		if max > 1000 {
			max = 1000
		}
		recursive := true
		if _, ok := kit.Args(req)["recursive"]; ok {
			recursive = kit.BoolArg(req, "recursive")
		}
		out := make([]searchEntry, 0, max)
		truncated := false
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if path != p && d.IsDir() && !recursive {
				return filepath.SkipDir
			}
			if path == p {
				return nil
			}
			ok, merr := filepath.Match(pattern, d.Name())
			if merr != nil || !ok {
				return nil
			}
			sz := int64(0)
			if info, ierr := d.Info(); ierr == nil {
				sz = info.Size()
			}
			out = append(out, searchEntry{Path: path, Name: d.Name(), IsDir: d.IsDir(), Size: sz})
			if len(out) >= max {
				truncated = true
				return filepath.SkipAll
			}
			return nil
		})
		if err != nil {
			return kit.ResultError("search: %v", err), nil
		}
		return kit.ResultJSON(map[string]any{
			"path":      p,
			"pattern":   pattern,
			"count":     len(out),
			"truncated": truncated,
			"entries":   out,
		}), nil
	}
}

func fileInfo(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, err := kit.RequireStr(req, "path")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		p, err := Resolve(raw, Roots(&k.Cfg.Tools))
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		info, err := os.Stat(p)
		if err != nil {
			return kit.ResultError("stat: %v", err), nil
		}
		return kit.ResultJSON(map[string]any{
			"name":     info.Name(),
			"path":     p,
			"size":     info.Size(),
			"mode":     info.Mode().String(),
			"is_dir":   info.IsDir(),
			"mod_time": info.ModTime().UTC().Format(time.RFC3339),
		}), nil
	}
}

func moveFile(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		srcRaw, err := kit.RequireStr(req, "source")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		dstRaw, err := kit.RequireStr(req, "destination")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		roots := Roots(&k.Cfg.Tools)
		src, err := Resolve(srcRaw, roots)
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		dst, err := Resolve(dstRaw, roots)
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, syscall.EXDEV) {
				if cerr := copyThenRemove(src, dst); cerr != nil {
					return kit.ResultError("move: %v", cerr), nil
				}
			} else {
				return kit.ResultError("move: %v", err), nil
			}
		}
		return kit.ResultJSON(map[string]any{"source": src, "destination": dst, "moved": true}), nil
	}
}

func copyThenRemove(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("cross-device move only supports regular files")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return os.Remove(src)
}

func deleteFile(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, err := kit.RequireStr(req, "path")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		roots := Roots(&k.Cfg.Tools)
		p, err := Resolve(raw, roots)
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		for _, r := range roots {
			if resolveRoot(r) == p {
				return kit.ResultError("refusing to delete sandbox root %q", p), nil
			}
		}
		recursive := false
		if _, ok := kit.Args(req)["recursive"]; ok {
			recursive = kit.BoolArg(req, "recursive")
		}
		if recursive {
			err = os.RemoveAll(p)
		} else {
			err = os.Remove(p)
		}
		if err != nil {
			return kit.ResultError("delete: %v", err), nil
		}
		return kit.ResultJSON(map[string]any{"path": p, "deleted": true}), nil
	}
}
