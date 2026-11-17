package device

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

func TestGetLocationInvalidProvider(t *testing.T) {
	k := &kit.Kit{Cfg: config.Default()}
	res := call(getLocation(k), map[string]any{"provider": "wifi"})
	if !res.IsError {
		t.Fatal("expected error")
	}
	if !strings.Contains(textOf(res), "invalid provider") {
		t.Fatalf("got %q", textOf(res))
	}
}

func TestGetLocationInvalidRequest(t *testing.T) {
	k := &kit.Kit{Cfg: config.Default()}
	res := call(getLocation(k), map[string]any{"request": "always"})
	if !res.IsError {
		t.Fatal("expected error")
	}
	if !strings.Contains(textOf(res), "invalid request") {
		t.Fatalf("got %q", textOf(res))
	}
}

func TestGetLocationSuccess(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "termux-location")
	body := "#!/bin/sh\necho '{\"latitude\":45.0,\"longitude\":11.0}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	k := &kit.Kit{Cfg: config.Default()}
	res := call(getLocation(k), map[string]any{"provider": "gps", "request": "once"})
	if res.IsError {
		t.Fatalf("error: %s", textOf(res))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(textOf(res)), &out); err != nil {
		t.Fatal(err)
	}
	if out["latitude"] != 45.0 {
		t.Fatalf("latitude = %v", out["latitude"])
	}
}
