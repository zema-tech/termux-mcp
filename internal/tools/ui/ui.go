// Package ui implements Module F: UI automation (dangerous tier, opt-in).
// Commands (input, screencap, uiautomator) come from android-tools and need
// wireless debugging or root on most setups.
package ui

import (
	"context"
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"termux-mcp/internal/registry"
	"termux-mcp/internal/tools/files"
	"termux-mcp/internal/tools/kit"
)

// All returns the UI automation module's tools.
func All(k *kit.Kit) []registry.Tool {
	return []registry.Tool{
		{Def: mcp.NewTool("take_screenshot",
			mcp.WithDescription("Capture the screen to a PNG file via screencap."),
			mcp.WithString("output", mcp.Required(), mcp.Description("Output PNG path (inside an allowed root)"))),
			Handler: takeScreenshot(k),
			Meta:    registry.Meta{Module: "ui", Tier: registry.TierDangerous}},

		{Def: mcp.NewTool("dump_ui",
			mcp.WithDescription("Dump the current UI hierarchy XML via uiautomator."),
			mcp.WithString("output", mcp.Description("Where uiautomator writes the dump (default /sdcard/window_dump.xml)"))),
			Handler: dumpUI(k),
			Meta:    registry.Meta{Module: "ui", Tier: registry.TierDangerous}},

		{Def: mcp.NewTool("tap_screen",
			mcp.WithDescription("Tap at screen coordinates via input tap."),
			mcp.WithNumber("x", mcp.Required(), mcp.Description("X in pixels"), mcp.Min(0)),
			mcp.WithNumber("y", mcp.Required(), mcp.Description("Y in pixels"), mcp.Min(0))),
			Handler: tapScreen(k),
			Meta:    registry.Meta{Module: "ui", Tier: registry.TierDangerous}},

		{Def: mcp.NewTool("swipe_screen",
			mcp.WithDescription("Swipe from one point to another via input swipe."),
			mcp.WithNumber("x1", mcp.Required(), mcp.Description("Start X"), mcp.Min(0)),
			mcp.WithNumber("y1", mcp.Required(), mcp.Description("Start Y"), mcp.Min(0)),
			mcp.WithNumber("x2", mcp.Required(), mcp.Description("End X"), mcp.Min(0)),
			mcp.WithNumber("y2", mcp.Required(), mcp.Description("End Y"), mcp.Min(0)),
			mcp.WithNumber("duration", mcp.Description("Duration in ms (default 300)"), mcp.Min(0))),
			Handler: swipeScreen(k),
			Meta:    registry.Meta{Module: "ui", Tier: registry.TierDangerous}},

		{Def: mcp.NewTool("long_press",
			mcp.WithDescription("Long-press at screen coordinates via input swipe (same point, held duration)."),
			mcp.WithNumber("x", mcp.Required(), mcp.Description("X in pixels"), mcp.Min(0)),
			mcp.WithNumber("y", mcp.Required(), mcp.Description("Y in pixels"), mcp.Min(0)),
			mcp.WithNumber("duration", mcp.Description("Hold duration in ms (default 800, clamped 100-10000)"), mcp.Min(0))),
			Handler: longPress(k),
			Meta:    registry.Meta{Module: "ui", Tier: registry.TierDangerous}},

		{Def: mcp.NewTool("click_by_text",
			mcp.WithDescription("Find a UI node by text via uiautomator dump and tap its center."),
			mcp.WithString("text", mcp.Required(), mcp.Description("Text to match")),
			mcp.WithBoolean("exact", mcp.Description("Require exact match (default false = substring, case-insensitive)")),
			mcp.WithString("output", mcp.Description("Where uiautomator writes the dump (default /sdcard/window_dump.xml)"))),
			Handler: clickByText(k),
			Meta:    registry.Meta{Module: "ui", Tier: registry.TierDangerous}},

		{Def: mcp.NewTool("input_text",
			mcp.WithDescription("Type text into the focused field via input text."),
			mcp.WithString("text", mcp.Required(), mcp.Description("Text to type"))),
			Handler: inputText(k),
			Meta:    registry.Meta{Module: "ui", Tier: registry.TierDangerous}},

		{Def: mcp.NewTool("input_keyevent",
			mcp.WithDescription("Send a key event via input keyevent."),
			mcp.WithString("keycode", mcp.Required(), mcp.Description("Keycode number or name, e.g. 4, KEYCODE_BACK, KEYCODE_ENTER"))),
			Handler: inputKeyevent(k),
			Meta:    registry.Meta{Module: "ui", Tier: registry.TierDangerous}},

		{Def: mcp.NewTool("go_home",
			mcp.WithDescription("Press the Home key (input keyevent 3).")),
			Handler: keyevent(k, "3"),
			Meta:    registry.Meta{Module: "ui", Tier: registry.TierDangerous}},

		{Def: mcp.NewTool("go_back",
			mcp.WithDescription("Press the Back key (input keyevent 4).")),
			Handler: keyevent(k, "4"),
			Meta:    registry.Meta{Module: "ui", Tier: registry.TierDangerous}},

		{Def: mcp.NewTool("open_recent_apps",
			mcp.WithDescription("Open the recent-apps switcher (input keyevent 187).")),
			Handler: keyevent(k, "187"),
			Meta:    registry.Meta{Module: "ui", Tier: registry.TierDangerous}},
	}
}

func inputRun(k *kit.Kit, ctx context.Context, args []string) (*mcp.CallToolResult, error) {
	res, err := k.Run(ctx, "input", args, 0)
	if err != nil {
		return kit.ResultError("%v (install android-tools and enable wireless debugging)", err), nil
	}
	if res.ExitCode != 0 {
		return kit.ResultError("input exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), nil
	}
	return kit.ResultText("ok"), nil
}

func takeScreenshot(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		out, err := kit.RequireStr(req, "output")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		p, err := files.Resolve(out, files.Roots(&k.Cfg.Tools))
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		res, err := k.Run(ctx, "screencap", []string{"-p", p}, 0)
		if err != nil {
			return kit.ResultError("%v (install android-tools)", err), nil
		}
		if res.ExitCode != 0 {
			return kit.ResultError("screencap exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), nil
		}
		if fi, err := os.Stat(p); err == nil {
			return kit.ResultText("screenshot saved to %s (%d bytes)", p, fi.Size()), nil
		}
		return kit.ResultText("screenshot saved to %s", p), nil
	}
}

func dumpUI(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		out := kit.StrArg(req, "output")
		if out == "" {
			out = "/sdcard/window_dump.xml"
		}
		res, err := k.Run(ctx, "uiautomator", []string{"dump", out}, 0)
		if err != nil {
			return kit.ResultError("%v (install android-tools)", err), nil
		}
		if res.ExitCode != 0 {
			return kit.ResultError("uiautomator exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), nil
		}
		data, err := os.ReadFile(out)
		if err != nil {
			return kit.ResultError("read dump: %v", err), nil
		}
		if int64(len(data)) > k.Cfg.Exec.MaxOutputBytes {
			data = data[:k.Cfg.Exec.MaxOutputBytes]
		}
		return kit.ResultText("%s", string(data)), nil
	}
}

func tapScreen(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		x := int(math.Round(kit.NumArg(req, "x")))
		y := int(math.Round(kit.NumArg(req, "y")))
		return inputRun(k, ctx, []string{"tap", itoa(x), itoa(y)})
	}
}

func swipeScreen(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := []string{"swipe",
			itoa(int(math.Round(kit.NumArg(req, "x1")))),
			itoa(int(math.Round(kit.NumArg(req, "y1")))),
			itoa(int(math.Round(kit.NumArg(req, "x2")))),
			itoa(int(math.Round(kit.NumArg(req, "y2")))),
		}
		if d := int(math.Round(kit.NumArg(req, "duration"))); d > 0 {
			args = append(args, itoa(d))
		}
		return inputRun(k, ctx, args)
	}
}

func longPress(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		x := itoa(int(math.Round(kit.NumArg(req, "x"))))
		y := itoa(int(math.Round(kit.NumArg(req, "y"))))
		d, ok := kit.Args(req)["duration"]
		ms := 800
		if ok {
			if f, isNum := d.(float64); isNum {
				ms = int(math.Round(f))
			}
		}
		if ms < 100 {
			ms = 100
		}
		if ms > 10000 {
			ms = 10000
		}
		return inputRun(k, ctx, []string{"swipe", x, y, x, y, itoa(ms)})
	}
}

// uiNode is the subset of a uiautomator node we need to locate text.
type uiNode struct {
	Text   string   `xml:"text,attr"`
	Bounds string   `xml:"bounds,attr"`
	Nodes  []uiNode `xml:"node"`
}

func collectNodes(n uiNode, out *[]uiNode) {
	*out = append(*out, n)
	for _, c := range n.Nodes {
		collectNodes(c, out)
	}
}

func clickByText(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, err := kit.RequireStr(req, "text")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		exact := false
		if _, ok := kit.Args(req)["exact"]; ok {
			exact = kit.BoolArg(req, "exact")
		}
		out := kit.StrArg(req, "output")
		if out == "" {
			out = "/sdcard/window_dump.xml"
		}

		res, err := k.Run(ctx, "uiautomator", []string{"dump", out}, 0)
		if err != nil {
			return kit.ResultError("%v (install android-tools)", err), nil
		}
		if res.ExitCode != 0 {
			return kit.ResultError("uiautomator exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), nil
		}

		data, err := os.ReadFile(out)
		if err != nil {
			return kit.ResultError("read dump: %v", err), nil
		}

		var root struct {
			Nodes []uiNode `xml:"node"`
		}
		if err := xml.Unmarshal(data, &root); err != nil {
			return kit.ResultError("parse dump: %v", err), nil
		}

		var nodes []uiNode
		for _, n := range root.Nodes {
			collectNodes(n, &nodes)
		}

		needle := strings.ToLower(text)
		var match *uiNode
		matches := 0
		for i := range nodes {
			nt := nodes[i].Text
			if nt == "" {
				continue
			}
			hit := nt == text
			if !exact {
				hit = strings.Contains(strings.ToLower(nt), needle)
			}
			if hit {
				matches++
				if match == nil {
					match = &nodes[i]
				}
			}
		}
		if match == nil {
			return kit.ResultError("no node with text %q found in %s", text, out), nil
		}

		var x1, y1, x2, y2 int
		if _, err := fmt.Sscanf(match.Bounds, "[%d,%d][%d,%d]", &x1, &y1, &x2, &y2); err != nil {
			return kit.ResultError("invalid bounds %q: %v", match.Bounds, err), nil
		}
		cx := (x1 + x2) / 2
		cy := (y1 + y2) / 2

		tres, terr := k.Run(ctx, "input", []string{"tap", itoa(cx), itoa(cy)}, 0)
		if terr != nil {
			return kit.ResultError("%v (install android-tools and enable wireless debugging)", terr), nil
		}
		if tres.ExitCode != 0 {
			return kit.ResultError("input exited %d: %s", tres.ExitCode, strings.TrimSpace(tres.Stderr)), nil
		}
		return kit.ResultJSON(map[string]any{
			"text":    match.Text,
			"x":       cx,
			"y":       cy,
			"matches": matches,
		}), nil
	}
}

func inputText(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, err := kit.RequireStr(req, "text")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		// Android input text splits on spaces; encode them as %s.
		return inputRun(k, ctx, []string{"text", strings.ReplaceAll(text, " ", "%s")})
	}
}

func inputKeyevent(k *kit.Kit) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		code, err := kit.RequireStr(req, "keycode")
		if err != nil {
			return kit.ResultError("%v", err), nil
		}
		return inputRun(k, ctx, []string{"keyevent", code})
	}
}

func keyevent(k *kit.Kit, code string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return inputRun(k, ctx, []string{"keyevent", code})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
