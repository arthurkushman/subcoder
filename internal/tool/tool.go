package tool

import (
	"context"
	"encoding/json"
	"sort"
)

type Context struct {
	Directory string
	SessionID string
	Agent     string
	Todos     TodoStore
}

type TodoItem struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

type TodoStore interface {
	SetTodos(todos []TodoItem)
	Todos() []TodoItem
}

type Result struct {
	Title    string         `json:"title"`
	Output   string         `json:"output"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type Spec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage, env *Context) (Result, error)
}

type Registry struct {
	byName map[string]Tool
	order  []string
}

func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{byName: map[string]Tool{}}
	for _, t := range tools {
		r.Add(t)
	}
	return r
}

func (r *Registry) Add(t Tool) {
	if _, exists := r.byName[t.Name()]; !exists {
		r.order = append(r.order, t.Name())
	}
	r.byName[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

func (r *Registry) Names() []string {
	return append([]string(nil), r.order...)
}

func (r *Registry) All() []Tool {
	tools := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		tools = append(tools, r.byName[name])
	}
	return tools
}

// Subset returns a registry containing only the named tools. Unknown names
// are ignored.
func (r *Registry) Subset(names []string) *Registry {
	if len(names) == 0 {
		return r
	}
	subset := NewRegistry()
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if t, ok := r.byName[name]; ok {
			subset.Add(t)
		}
	}
	return subset
}

// Specs returns provider tool specs. An empty allow list returns all tools.
func (r *Registry) Specs(allow []string) []Spec {
	registry := r.Subset(allow)
	specs := make([]Spec, 0, len(registry.order))
	for _, name := range registry.order {
		t := registry.byName[name]
		specs = append(specs, Spec{Name: t.Name(), Description: t.Description(), Parameters: t.Parameters()})
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs
}

func schema(properties map[string]any, required ...string) json.RawMessage {
	obj := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		obj["required"] = required
	}
	data, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}
	return data
}

func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func intProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func boolProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}
