package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Facets-cloud/bx/internal/exit"
)

// mcpClient is an app whose MCP servers live in a JSON file under
// "mcpServers". Its app folder must exist for bx to touch the file.
type mcpClient struct {
	Name   string
	AppDir string // relative to $HOME; the app is installed if it exists
	Config string // relative to $HOME
}

var mcpClients = []mcpClient{
	{"Claude Desktop", "Library/Application Support/Claude", "Library/Application Support/Claude/claude_desktop_config.json"},
	{"Cursor", ".cursor", ".cursor/mcp.json"},
}

// cmdMCPInstall adds bx to the MCP config of every installed client that
// reads a config file. Clients with their own CLI get the command to run.
func cmdMCPInstall(e env) error {
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	var done []string
	for _, c := range mcpClients {
		if _, err := os.Stat(filepath.Join(home, c.AppDir)); err != nil {
			continue
		}
		path := filepath.Join(home, c.Config)
		if err := addMCPServer(path, self); err != nil {
			return exit.New(exit.User, "add bx to %s (%s): %v", c.Name, path, err)
		}
		done = append(done, c.Name+": "+path)
	}
	cli := []string{"claude mcp add -s user bx -- " + self + " mcp", "codex mcp add bx -- " + self + " mcp"}
	switch {
	case e.o.quiet:
	case e.o.json:
		e.o.printJSON(map[string]any{"installed": done, "commands_for_cli_clients": cli})
	default:
		for _, d := range done {
			fmt.Fprintln(e.o.w, "added bx to", d)
		}
		if len(done) == 0 {
			fmt.Fprintln(e.o.w, "found neither Claude Desktop nor Cursor")
		}
		fmt.Fprintln(e.o.w, "Restart those apps to load it. Claude Code and Codex use the bx skill;")
		fmt.Fprintln(e.o.w, "to give them the MCP server too, run:")
		for _, c := range cli {
			fmt.Fprintln(e.o.w, "  "+c)
		}
	}
	return nil
}

// addMCPServer sets mcpServers.bx in a client's JSON config, keeping
// every other setting. A missing file is created.
func addMCPServer(path, bin string) error {
	cfg := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&cfg); err != nil {
			return fmt.Errorf("not valid JSON, so bx leaves it alone: %v", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["bx"] = map[string]any{"command": bin, "args": []string{"mcp"}}
	cfg["mcpServers"] = servers
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".bx-tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
