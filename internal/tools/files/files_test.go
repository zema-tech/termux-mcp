package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"termux-mcp/internal/config"
	"termux-mcp/internal/tools/kit"
)

func testKit(t *testing.T, roots ...string) *kit.Kit {
	t.Helper()
	if len(roots) == 0 {
		roots = []string{t.TempDir()}
	}
	cfg := config.Default()
	cfg.Tools.FileRoots = roots
	return &kit.Kit{Cfg: cfg}
}

func call(h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		panic(err)
	}
	return res
}

func textOf(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestResolveEmpty(t *testing.T) {
	_, err := Resolve("", []string{"/tmp"})
	if err == nil || !strings.Contains(err.Error(), "empty path") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveOutsideRoots(t *testing.T) {
	root := t.TempDir()
	_, err := Resolve("/etc/passwd", []string{root})
	if err == nil || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchFilesSuccess(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.log"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "c.txt"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}

	k := testKit(t, root)
	res := call(searchFiles(k), map[string]any{"path": root, "pattern": "*.txt"})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &out); err != nil {
		t.Fatal(err)
	}
	if int(out["count"].(float64)) != 2 {
		t.Fatalf("count = %v, want 2", out["count"])
	}
}

func TestSearchFilesInvalidPattern(t *testing.T) {
	root := t.TempDir()
	k := testKit(t, root)
	res := call(searchFiles(k), map[string]any{"path": root, "pattern": "["})
	if !res.IsError {
		t.Fatal("expected error for invalid pattern")
	}
	if !strings.Contains(textOf(res), "invalid pattern") {
		t.Fatalf("got %q", textOf(res))
	}
}

func TestSearchFilesOutsideRoot(t *testing.T) {
	k := testKit(t, t.TempDir())
	res := call(searchFiles(k), map[string]any{"path": "/etc"})
	if !res.IsError {
		t.Fatal("expected outside roots error")
	}
}

func TestFileInfoSuccess(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := testKit(t, root)
	res := call(fileInfo(k), map[string]any{"path": p})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &out); err != nil {
		t.Fatal(err)
	}
	if out["name"] != "hello.txt" {
		t.Fatalf("name = %v", out["name"])
	}
	if out["is_dir"] != false {
		t.Fatalf("is_dir = %v", out["is_dir"])
	}
	if int(out["size"].(float64)) != 5 {
		t.Fatalf("size = %v", out["size"])
	}
}

func TestMoveFileSuccess(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src.txt")
	dst := filepath.Join(root, "dst.txt")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := testKit(t, root)
	res := call(moveFile(k), map[string]any{"source": src, "destination": dst})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source still exists")
	}
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != "data" {
		t.Fatalf("dst content = %q err=%v", b, err)
	}
}

func TestDeleteFileSuccess(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "gone.txt")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := testKit(t, root)
	res := call(deleteFile(k), map[string]any{"path": p})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("file still exists")
	}
}

func TestDeleteFileRefuseRoot(t *testing.T) {
	root := t.TempDir()
	k := testKit(t, root)
	res := call(deleteFile(k), map[string]any{"path": root})
	if !res.IsError {
		t.Fatal("expected refuse root error")
	}
	if !strings.Contains(textOf(res), "refusing to delete sandbox root") {
		t.Fatalf("got %q", textOf(res))
	}
}

func TestDeleteFileRecursive(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := testKit(t, root)
	res := call(deleteFile(k), map[string]any{"path": dir, "recursive": true})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("dir still exists")
	}
}
