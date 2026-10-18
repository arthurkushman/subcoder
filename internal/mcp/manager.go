package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/arthurkushman/subcoder/internal/tool"
)

type Manager struct {
	clients []Client
	tools   []tool.Tool
}

// Start connects to all enabled MCP servers, lists their tools and returns a
// manager. Servers that fail to start are reported in the returned error but
// do not prevent other servers from working.
func Start(ctx context.Context, servers map[string]config.MCPServerConfig) (*Manager, error) {
	manager := &Manager{}

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var failures []string
	for _, name := range names {
		server := servers[name]
		if !server.IsEnabled() {
			continue
		}

		var client Client
		var err error
		switch server.Type {
		case "remote", "http", "sse":
			if server.URL == "" {
				err = fmt.Errorf("mcp: server %q is missing url", name)
				break
			}
			client, err = NewHTTPClient(ctx, name, server.URL)
		default:
			client, err = StartStdio(ctx, name, server.Command, server.Environment)
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
			continue
		}

		info, err := client.ListTools(ctx)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
			client.Close()
			continue
		}

		manager.clients = append(manager.clients, client)
		for _, toolInfo := range info {
			manager.tools = append(manager.tools, &serverTool{
				client:      client,
				server:      name,
				name:        toolInfo.Name,
				description: toolInfo.Description,
				schema:      toolInfo.InputSchema,
			})
		}
	}

	if len(failures) > 0 {
		return manager, fmt.Errorf("mcp: %v", failures)
	}
	return manager, nil
}

func (m *Manager) Tools() []tool.Tool {
	return append([]tool.Tool(nil), m.tools...)
}

func (m *Manager) Clients() []Client {
	return append([]Client(nil), m.clients...)
}

func (m *Manager) Close() {
	for _, client := range m.clients {
		_ = client.Close()
	}
}

type serverTool struct {
	client      Client
	server      string
	name        string
	description string
	schema      json.RawMessage
}

func (t *serverTool) Name() string {
	return t.server + "_" + t.name
}

func (t *serverTool) Description() string {
	if t.description != "" {
		return t.description
	}
	return fmt.Sprintf("MCP tool %q from server %q", t.name, t.server)
}

func (t *serverTool) Parameters() json.RawMessage {
	if len(t.schema) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return t.schema
}

func (t *serverTool) Execute(ctx context.Context, args json.RawMessage, env *tool.Context) (tool.Result, error) {
	output, err := t.client.CallTool(ctx, t.name, args)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Title:  fmt.Sprintf("%s/%s", t.server, t.name),
		Output: output,
		Metadata: map[string]any{
			"server": t.server,
			"tool":   t.name,
		},
	}, nil
}
