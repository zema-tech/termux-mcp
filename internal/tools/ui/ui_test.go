package ui

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

func writeFakeInput(t *testing.T, dir string, logName string) {
	t.Helper()
	script := filepath.Join(dir, "input")
	body := "#!/bin/sh\necho \"$@\" >> \"" + filepath.Join(dir, logName) + "\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLongPressDefaultDuration(t *testing.T) {
	dir := t.TempDir()
	writeFakeInput(t, dir, "args.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	k := &kit.Kit{Cfg: config.Default()}
	res := call(longPress(k), map[string]any{"x": 10.0, "y": 20.0})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	b, err := os.ReadFile(filepath.Join(dir, "args.log"))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(b))
	if !strings.HasSuffix(line, " 800") {
		t.Fatalf("args = %q, want duration 800", line)
	}
}

func TestLongPressClampLow(t *testing.T) {
	dir := t.TempDir()
	writeFakeInput(t, dir, "args.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	k := &kit.Kit{Cfg: config.Default()}
	res := call(longPress(k), map[string]any{"x": 1.0, "y": 2.0, "duration": 50.0})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	b, _ := os.ReadFile(filepath.Join(dir, "args.log"))
	if !strings.HasSuffix(strings.TrimSpace(string(b)), " 100") {
		t.Fatalf("args = %q, want duration 100", string(b))
	}
}

func TestLongPressClampHigh(t *testing.T) {
	dir := t.TempDir()
	writeFakeInput(t, dir, "args.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	k := &kit.Kit{Cfg: config.Default()}
	res := call(longPress(k), map[string]any{"x": 1.0, "y": 2.0, "duration": 20000.0})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	b, _ := os.ReadFile(filepath.Join(dir, "args.log"))
	if !strings.HasSuffix(strings.TrimSpace(string(b)), " 10000") {
		t.Fatalf("args = %q, want duration 10000", string(b))
	}
}

func TestClickByTextSuccess(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "dump.xml")
	xmlBody := `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <node text="" bounds="[0,0][100,100]">
    <node text="Hello World" bounds="[10,20][90,60]" />
  </node>
</hierarchy>`
	if err := os.WriteFile(dump, []byte(xmlBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake uiautomator that just exits 0 (dump already written).
	uiauto := filepath.Join(dir, "uiautomator")
	if err := os.WriteFile(uiauto, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeInput(t, dir, "tap.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	k := &kit.Kit{Cfg: config.Default()}
	res := call(clickByText(k), map[string]any{"text": "hello", "output": dump})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &out); err != nil {
		t.Fatal(err)
	}
	if out["text"] != "Hello World" {
		t.Fatalf("text = %v", out["text"])
	}
	// center of [10,20][90,60] = (50, 40)
	if int(out["x"].(float64)) != 50 || int(out["y"].(float64)) != 40 {
		t.Fatalf("xy = %v,%v", out["x"], out["y"])
	}
}

func TestClickByTextNotFound(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "dump.xml")
	xmlBody := `<?xml version='1.0'?><hierarchy><node text="Other" bounds="[0,0][10,10]"/></hierarchy>`
	if err := os.WriteFile(dump, []byte(xmlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	uiauto := filepath.Join(dir, "uiautomator")
	if err := os.WriteFile(uiauto, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	k := &kit.Kit{Cfg: config.Default()}
	res := call(clickByText(k), map[string]any{"text": "Missing", "output": dump})
	if !res.IsError {
		t.Fatal("expected not found error")
	}
	if !strings.Contains(textOf(res), "no node with text") {
		t.Fatalf("got %q", textOf(res))
	}
}
