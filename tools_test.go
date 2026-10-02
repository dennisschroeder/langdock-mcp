package main

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type recorded struct {
	Method      string
	Path        string
	Query       string
	Auth        string
	ContentType string
	Body        []byte
}

// fakeLangdock records every request and answers from a per-route table.
type fakeLangdock struct {
	mu        sync.Mutex
	requests  []recorded
	responses map[string]fakeResponse // key: "METHOD /path"
}

type fakeResponse struct {
	status int
	body   string
}

func (f *fakeLangdock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, recorded{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), body})
	resp, ok := f.responses[r.Method+" "+r.URL.EscapedPath()]
	f.mu.Unlock()
	if !ok {
		resp = fakeResponse{http.StatusOK, `{"ok":true}`}
	}
	w.WriteHeader(resp.status)
	io.WriteString(w, resp.body)
}

func (f *fakeLangdock) last(t *testing.T) recorded {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("no request reached the fake Langdock API")
	}
	return f.requests[len(f.requests)-1]
}

// connect wires a real SDK client to the server over in-memory transports,
// so tests exercise schema validation and the protocol negotiation too.
func connect(t *testing.T, apiKey string, responses map[string]fakeResponse) (*mcp.ClientSession, *fakeLangdock) {
	t.Helper()
	fake := &fakeLangdock{responses: responses}
	api := httptest.NewServer(fake)
	t.Cleanup(api.Close)

	srv := NewServer(NewClient(api.URL, apiKey))
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.mcp.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, fake
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

func TestNegotiatesLatestProtocol(t *testing.T) {
	cs, _ := connect(t, "k", nil)
	if got := cs.InitializeResult().ProtocolVersion; got != "2026-07-28" {
		t.Fatalf("negotiated %s, want 2026-07-28", got)
	}
}

func TestToolsMapToEndpoints(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	const aid = "22222222-2222-2222-2222-222222222222"
	tests := []struct {
		tool     string
		args     map[string]any
		method   string
		path     string
		wantBody string // JSON the request body must equal, "" for none
	}{
		{"list_integrations", map[string]any{}, "GET", "/integrations/v1/get", ""},
		{"get_integration", map[string]any{"integrationId": id}, "GET", "/integrations/v1/" + id, ""},
		{"create_integration", map[string]any{"name": "CRM", "description": "d"}, "POST", "/integrations/v1/create", `{"name":"CRM","description":"d"}`},
		{"update_integration", map[string]any{"integrationId": id, "color": "#fff"}, "PATCH", "/integrations/v1/" + id, `{"color":"#fff"}`},
		{"update_integration_auth", map[string]any{"integrationId": id, "authType": "API_KEY", "authFields": []any{map[string]any{"slug": "api_key", "label": "API Key", "type": "PASSWORD", "required": true}}}, "PATCH", "/integrations/v1/" + id + "/auth", `{"authType":"API_KEY","authFields":[{"slug":"api_key","label":"API Key","type":"PASSWORD","required":true}]}`},
		{"get_integration_icon", map[string]any{"integrationId": id}, "GET", "/integrations/v1/" + id + "/icon", ""},
		{"create_action", map[string]any{"integrationId": id, "name": "Get deal", "code": "return 1", "requiresConfirmation": false, "inputFields": []any{map[string]any{"label": "Deal ID", "type": "ID", "required": true}}}, "POST", "/integrations/v1/" + id + "/actions/create", `{"name":"Get deal","code":"return 1","inputFields":[{"label":"Deal ID","type":"ID","required":true}],"requiresConfirmation":false}`},
		{"delete_action", map[string]any{"integrationId": id, "actionId": aid}, "DELETE", "/integrations/v1/" + id + "/actions/" + aid, ""},
		{"create_trigger", map[string]any{"integrationId": id, "name": "New deal", "pollingCode": "return []"}, "POST", "/integrations/v1/" + id + "/triggers/create", `{"name":"New deal","pollingCode":"return []"}`},
		{"update_trigger", map[string]any{"integrationId": id, "triggerId": aid, "name": "New deal"}, "PUT", "/integrations/v1/" + id + "/triggers/" + aid, `{"name":"New deal"}`},
		{"delete_trigger", map[string]any{"integrationId": id, "triggerId": aid}, "DELETE", "/integrations/v1/" + id + "/triggers/" + aid, ""},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			cs, fake := connect(t, "secret", nil)
			text, isErr := callTool(t, cs, tt.tool, tt.args)
			if isErr {
				t.Fatalf("tool error: %s", text)
			}
			req := fake.last(t)
			if req.Method != tt.method || req.Path != tt.path {
				t.Errorf("got %s %s, want %s %s", req.Method, req.Path, tt.method, tt.path)
			}
			if req.Auth != "Bearer secret" {
				t.Errorf("Authorization = %q", req.Auth)
			}
			assertJSONEqual(t, req.Body, tt.wantBody)
		})
	}
}

func TestUpdateActionPreservesUnspecifiedFields(t *testing.T) {
	const id, aid = "i1", "a1"
	current := `{"integration":{"id":"i1","actions":[{"id":"a1","name":"Get deal","slug":"get_deal","description":"Fetches a deal","code":"old","order":0,"requiresConfirmation":true,
		"inputFields":[{"slug":"deal_id","label":"Deal ID","type":"ID","description":"","placeholder":null,"required":true,"order":0,"options":null,"allowMultiSelect":null,"contextActionId":null}]}]}}`
	cs, fake := connect(t, "k", map[string]fakeResponse{"GET /integrations/v1/i1": {200, current}})

	text, isErr := callTool(t, cs, "update_action", map[string]any{"integrationId": id, "actionId": aid, "code": "new"})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	req := fake.last(t)
	if req.Method != "PUT" || req.Path != "/integrations/v1/i1/actions/a1" {
		t.Fatalf("got %s %s", req.Method, req.Path)
	}
	assertJSONEqual(t, req.Body, `{"name":"Get deal","description":"Fetches a deal","code":"new","inputFields":[{"label":"Deal ID","type":"ID","required":true}]}`)
}

func TestMergeAction(t *testing.T) {
	cur := currentAction{ID: "a", Name: "n", Description: "d", InputFields: []InputField{{Label: "x"}}}
	s := func(v string) *string { return &v }

	b, err := mergeAction(cur, UpdateActionInput{ClearDescription: true, ClearInputFields: true, Name: s("m")})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(b)
	assertJSONEqual(t, got, `{"name":"m","inputFields":[]}`)

	fields := []InputField{{Label: "y", Type: "TEXT"}}
	b, _ = mergeAction(cur, UpdateActionInput{InputFields: &fields, Description: s("e")})
	got, _ = json.Marshal(b)
	assertJSONEqual(t, got, `{"name":"n","description":"e","inputFields":[{"label":"y","type":"TEXT"}]}`)

	if _, err := mergeAction(cur, UpdateActionInput{ClearDescription: true, Description: s("e")}); err == nil {
		t.Error("expected error for description + clearDescription")
	}
	if _, err := mergeAction(cur, UpdateActionInput{ClearInputFields: true, InputFields: &fields}); err == nil {
		t.Error("expected error for inputFields + clearInputFields")
	}
}

func TestUpdateActionUnknownAction(t *testing.T) {
	cs, _ := connect(t, "k", map[string]fakeResponse{"GET /integrations/v1/i1": {200, `{"integration":{"actions":[]}}`}})
	text, isErr := callTool(t, cs, "update_action", map[string]any{"integrationId": "i1", "actionId": "nope", "code": "x"})
	if !isErr || !strings.Contains(text, "not found") {
		t.Fatalf("want not-found tool error, got %v %q", isErr, text)
	}
}

func TestSchemaRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"create_integration", map[string]any{"name": strings.Repeat("x", 41)}},
		{"update_integration_auth", map[string]any{"integrationId": "i", "authType": "BASIC"}},
		{"create_action", map[string]any{"integrationId": "i", "name": "a", "inputFields": []any{map[string]any{"label": "l", "type": "DATE"}}}},
		{"create_trigger", map[string]any{"integrationId": "i", "name": "t", "inputFields": []any{map[string]any{"label": "l", "type": "OBJECT"}}}},
		{"update_trigger", map[string]any{"integrationId": "i", "name": "t"}},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			cs, fake := connect(t, "k", nil)
			if _, isErr := callTool(t, cs, c.tool, c.args); !isErr {
				t.Fatal("expected validation error")
			}
			if len(fake.requests) != 0 {
				t.Fatal("invalid input reached the API")
			}
		})
	}
}

func TestAPIErrorsBecomeToolErrors(t *testing.T) {
	cs, _ := connect(t, "k", map[string]fakeResponse{
		"POST /integrations/v1/create": {http.StatusConflict, `{"error":"duplicate"}`},
		"GET /integrations/v1/get":     {http.StatusTooManyRequests, ``},
	})
	text, isErr := callTool(t, cs, "create_integration", map[string]any{"name": "CRM"})
	if !isErr || !strings.Contains(text, "409") || !strings.Contains(text, "already exists") || !strings.Contains(text, "duplicate") {
		t.Errorf("409: got %v %q", isErr, text)
	}
	text, isErr = callTool(t, cs, "list_integrations", map[string]any{})
	if !isErr || !strings.Contains(text, "rate limit") {
		t.Errorf("429: got %v %q", isErr, text)
	}
}

func TestMissingAPIKey(t *testing.T) {
	cs, fake := connect(t, "", nil)
	text, isErr := callTool(t, cs, "list_integrations", map[string]any{})
	if !isErr || !strings.Contains(text, "LANGDOCK_API_KEY") {
		t.Fatalf("got %v %q", isErr, text)
	}
	if len(fake.requests) != 0 {
		t.Fatal("request sent without API key")
	}
}

func TestReplaceIconUploadsMultipart(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	path := filepath.Join(t.TempDir(), "logo.png")
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatal(err)
	}
	cs, fake := connect(t, "k", nil)
	text, isErr := callTool(t, cs, "replace_integration_icon", map[string]any{"integrationId": "i1", "filePath": path})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	req := fake.last(t)
	if req.Method != "PUT" || req.Path != "/integrations/v1/i1/icon" {
		t.Fatalf("got %s %s", req.Method, req.Path)
	}
	_, params, err := mime.ParseMediaType(req.ContentType)
	if err != nil {
		t.Fatal(err)
	}
	part, err := multipart.NewReader(strings.NewReader(string(req.Body)), params["boundary"]).NextPart()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(part)
	if part.FormName() != "icon" || part.FileName() != "logo.png" || part.Header.Get("Content-Type") != "image/png" || string(data) != string(png) {
		t.Errorf("unexpected part: %s %s %s", part.FormName(), part.FileName(), part.Header.Get("Content-Type"))
	}
}

func TestReplaceIconRejectsNonImages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(path, []byte("hello"), 0o600)
	cs, fake := connect(t, "k", nil)
	for _, p := range []string{path, "relative.png"} {
		if _, isErr := callTool(t, cs, "replace_integration_icon", map[string]any{"integrationId": "i1", "filePath": p}); !isErr {
			t.Errorf("%s: expected error", p)
		}
	}
	if len(fake.requests) != 0 {
		t.Fatal("rejected file reached the API")
	}
}

func assertJSONEqual(t *testing.T, got []byte, want string) {
	t.Helper()
	if want == "" {
		if len(got) != 0 {
			t.Errorf("unexpected body %s", got)
		}
		return
	}
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("body %q is not JSON: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Errorf("body\n got %s\nwant %s", gb, wb)
	}
}

func TestCurrentActionSortsFieldsByOrder(t *testing.T) {
	var a currentAction
	raw := `{"id":"a","name":"n","inputFields":[{"slug":"b","label":"second","order":2},{"slug":"a","label":"first","order":1},{"slug":"c","label":"third","order":3}]}`
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range a.InputFields {
		got = append(got, f.Label)
	}
	if strings.Join(got, ",") != "first,second,third" {
		t.Fatalf("fields not sorted by order: %v", got)
	}
}
