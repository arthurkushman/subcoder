package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arthurkushman/subcoder/internal/agent"
	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/provider"
	"github.com/arthurkushman/subcoder/internal/provider/mock"
	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/arthurkushman/subcoder/internal/tool"
)

type testServer struct {
	server   *Server
	http     *httptest.Server
	provider *mock.Provider
	store    *session.Store
	dir      string
	engine   *permission.Engine
}

func newTestServer(t *testing.T, autoApprove bool) *testServer {
	t.Helper()

	dir := t.TempDir()
	store := session.NewStore(t.TempDir())
	mockProvider := mock.New("mock")
	providers := provider.NewRegistry()
	providers.Register(mockProvider)
	providers.SetDefault("mock/mock-small")

	engine := permission.New(nil, nil, autoApprove)
	runner := agent.NewRunner(providers, nil, store, engine)
	runner.Agents = agent.Builtins()
	runner.DefaultModel = "mock/mock-small"

	tools := tool.NewRegistry(
		tool.NewRead(), tool.NewWrite(), tool.NewBash(), tool.NewGrep(), tool.NewGlob(), tool.NewLs(), tool.NewTask(runner),
	)
	runner.Tools = tools

	server := New(Options{
		Providers:  providers,
		Sessions:   store,
		Runner:     runner,
		Agents:     agent.Builtins(),
		Permission: engine,
	})

	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	return &testServer{server: server, http: httpServer, provider: mockProvider, store: store, dir: dir, engine: engine}
}

func (ts *testServer) createSession(t *testing.T) string {
	t.Helper()
	body := strings.NewReader(`{"directory":"` + ts.dir + `"}`)
	resp, err := http.Post(ts.http.URL+"/sessions", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var sess session.Session
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

func readSSEEvents(t *testing.T, resp *http.Response, until func(session.Event) bool) []session.Event {
	t.Helper()
	defer resp.Body.Close()

	var events []session.Event
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event session.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			continue
		}
		events = append(events, event)
		if until != nil && until(event) {
			break
		}
	}
	return events
}

func TestHealthModelsAgents(t *testing.T) {
	ts := newTestServer(t, true)

	for _, path := range []string{"/health", "/models", "/agents"} {
		resp, err := http.Get(ts.http.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestSessionCRUD(t *testing.T) {
	ts := newTestServer(t, true)
	id := ts.createSession(t)

	resp, err := http.Get(ts.http.URL + "/sessions/" + id)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, _ = http.Get(ts.http.URL + "/sessions")
	listBody := struct {
		Sessions []session.Summary `json:"sessions"`
	}{}
	if err := json.NewDecoder(resp.Body).Decode(&listBody); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(listBody.Sessions) != 1 {
		t.Fatalf("sessions = %+v", listBody.Sessions)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.http.URL+"/sessions/"+id, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	resp, _ = http.Get(ts.http.URL + "/sessions/" + id)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestMessageStreaming(t *testing.T) {
	ts := newTestServer(t, true)
	id := ts.createSession(t)
	ts.provider.AddText("streamed answer")

	resp, err := http.Post(ts.http.URL+"/sessions/"+id+"/messages", "application/json",
		strings.NewReader(`{"text":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}

	events := readSSEEvents(t, resp, func(event session.Event) bool {
		return event.Type == session.EventSessionIdle
	})

	var sawText, sawIdle bool
	for _, event := range events {
		if event.Type == session.EventPartUpdated && event.Part != nil && event.Part.Type == session.PartText {
			sawText = true
		}
		if event.Type == session.EventSessionIdle {
			sawIdle = true
		}
	}
	if !sawText || !sawIdle {
		t.Fatalf("events = %+v", events)
	}

	loaded, err := ts.store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 || loaded.Messages[1].Text() != "streamed answer" {
		t.Fatalf("messages = %+v", loaded.Messages)
	}
}

func TestPermissionFlowOverHTTP(t *testing.T) {
	ts := newTestServer(t, false)
	id := ts.createSession(t)

	ts.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID: "call_1", Name: "bash", Arguments: `{"command":"echo allowed"}`,
	}}})
	ts.provider.AddText("finished")

	resp, err := http.Post(ts.http.URL+"/sessions/"+id+"/messages", "application/json",
		strings.NewReader(`{"text":"run"}`))
	if err != nil {
		t.Fatal(err)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var reqID string
	var events []session.Event
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event session.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			continue
		}
		events = append(events, event)
		if event.Type == session.EventPermission {
			data, _ := json.Marshal(event.Data)
			var req permission.Request
			if err := json.Unmarshal(data, &req); err != nil {
				t.Fatalf("permission data: %v", err)
			}
			reqID = req.ID

			replyBody := strings.NewReader(`{"reply":"always"}`)
			replyResp, err := http.Post(ts.http.URL+"/sessions/"+id+"/permissions/"+reqID, "application/json", replyBody)
			if err != nil {
				t.Fatal(err)
			}
			replyResp.Body.Close()
			if replyResp.StatusCode != http.StatusOK {
				t.Fatalf("reply status = %d", replyResp.StatusCode)
			}
		}
		if event.Type == session.EventSessionIdle {
			break
		}
	}
	resp.Body.Close()

	if reqID == "" {
		t.Fatalf("no permission request seen: %+v", events)
	}

	loaded, err := ts.store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	calls := loaded.Messages[1].ToolCalls()
	if len(calls) != 1 || calls[0].State != session.ToolCompleted {
		t.Fatalf("calls = %+v", calls)
	}
	if !strings.Contains(calls[0].Output, "allowed") {
		t.Fatalf("output = %q", calls[0].Output)
	}
}

func TestPermissionRejected(t *testing.T) {
	ts := newTestServer(t, false)
	id := ts.createSession(t)

	ts.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID: "call_1", Name: "bash", Arguments: `{"command":"rm -rf /"}`,
	}}})
	ts.provider.AddText("ok")

	resp, err := http.Post(ts.http.URL+"/sessions/"+id+"/messages", "application/json",
		strings.NewReader(`{"text":"run"}`))
	if err != nil {
		t.Fatal(err)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event session.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			continue
		}
		if event.Type == session.EventPermission {
			data, _ := json.Marshal(event.Data)
			var req permission.Request
			json.Unmarshal(data, &req)
			replyBody := strings.NewReader(`{"reply":"reject"}`)
			replyResp, _ := http.Post(ts.http.URL+"/sessions/"+id+"/permissions/"+req.ID, "application/json", replyBody)
			replyResp.Body.Close()
		}
		if event.Type == session.EventSessionIdle {
			break
		}
	}
	resp.Body.Close()

	loaded, _ := ts.store.Load(id)
	calls := loaded.Messages[1].ToolCalls()
	if calls[0].State != session.ToolError || !strings.Contains(calls[0].Error, "denied") {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestGlobalEventStream(t *testing.T) {
	ts := newTestServer(t, true)
	id := ts.createSession(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.http.URL+"/event", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	ts.provider.AddText("bus answer")
	_, err = http.Post(ts.http.URL+"/sessions/"+id+"/messages", "application/json", strings.NewReader(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}

	events := readSSEEvents(t, resp, func(event session.Event) bool {
		return event.Type == session.EventSessionIdle
	})
	found := false
	for _, event := range events {
		if event.Type == session.EventMessageUpdated && event.SessionID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("no message events on bus: %+v", events)
	}
}

func TestCompactEndpoint(t *testing.T) {
	ts := newTestServer(t, true)
	id := ts.createSession(t)

	ts.provider.AddText("first")
	resp, _ := http.Post(ts.http.URL+"/sessions/"+id+"/messages", "application/json", strings.NewReader(`{"text":"question"}`))
	readSSEEvents(t, resp, func(event session.Event) bool { return event.Type == session.EventSessionIdle })

	ts.provider.AddText("the summary")
	resp, err := http.Post(ts.http.URL+"/sessions/"+id+"/compact", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	loaded, _ := ts.store.Load(id)
	if len(loaded.Messages) != 2 || !loaded.Messages[0].Summary {
		t.Fatalf("messages = %+v", loaded.Messages)
	}
}

func TestExportEndpoint(t *testing.T) {
	ts := newTestServer(t, true)
	id := ts.createSession(t)

	resp, err := http.Get(ts.http.URL + "/sessions/" + id + "/export")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, id) {
		t.Fatalf("content disposition = %q", cd)
	}
	var sess session.Session
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	if sess.ID != id {
		t.Fatalf("exported id = %q", sess.ID)
	}
}

func TestAuthToken(t *testing.T) {
	ts := newTestServer(t, true)
	ts.server.token = "secret"

	resp, err := http.Get(ts.http.URL + "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.http.URL+"/sessions", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestWebUIAndDefaultDirectory(t *testing.T) {
	dir := t.TempDir()
	store := session.NewStore(t.TempDir())
	providers := provider.NewRegistry()
	providers.Register(mock.New("mock"))
	runner := agent.NewRunner(providers, nil, store, permission.New(nil, nil, true))
	runner.Agents = agent.Builtins()
	runner.DefaultModel = "mock/mock-small"
	runner.Tools = tool.NewRegistry()

	server := New(Options{
		Providers: providers,
		Sessions:  store,
		Runner:    runner,
		Agents:    agent.Builtins(),
		Directory: dir,
		WebUI:     true,
	})
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	resp, err := http.Get(httpServer.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("status = %d type = %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	body := new(strings.Builder)
	if _, err := io.Copy(body, resp.Body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), "SubCoder") || !strings.Contains(body.String(), "session/prompt") && !strings.Contains(body.String(), "/sessions/") {
		t.Fatalf("unexpected index")
	}

	create, err := http.Post(httpServer.URL+"/sessions", "application/json", strings.NewReader(`{"title":"web"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer create.Body.Close()
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", create.StatusCode)
	}
	var sess session.Session
	if err := json.NewDecoder(create.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	if sess.Directory != dir {
		t.Fatalf("directory = %q", sess.Directory)
	}
}
