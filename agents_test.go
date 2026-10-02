package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const testAgentID = "33333333-3333-3333-3333-333333333333"

// agentWithActions is a get_agent response; the connectionId is undocumented
// but seen in practice and must survive add/remove.
const agentWithActions = `{"status":"success","agent":{"id":"` + testAgentID + `","name":"A","actions":[
	{"actionId":"a1","requiresConfirmation":true,"connectionId":"c1"},
	{"actionId":"a2","requiresConfirmation":false,"connectionId":null}]}}`

func TestAgentToolsMapToEndpoints(t *testing.T) {
	tests := []struct {
		tool     string
		args     map[string]any
		method   string
		path     string
		query    string
		wantBody string
	}{
		{"get_agent", map[string]any{"agentId": testAgentID}, "GET", "/agent/v1/get", "agentId=" + testAgentID, ""},
		{"list_agent_models", map[string]any{}, "GET", "/agent/v1/models", "", ""},
		{"create_agent",
			map[string]any{"name": "Bot", "model": "claude-sonnet-5@default", "creativity": 0, "webSearch": false, "conversationStarters": []any{"Hi"}, "actions": []any{map[string]any{"actionId": "a1"}}},
			"POST", "/agent/v1/create", "",
			`{"name":"Bot","model":"claude-sonnet-5@default","creativity":0,"webSearch":false,"conversationStarters":["Hi"],"actions":[{"actionId":"a1"}]}`},
		{"update_agent",
			map[string]any{"agentId": testAgentID, "instruction": "Be brief"},
			"PATCH", "/agent/v1/update", "",
			`{"agentId":"` + testAgentID + `","instruction":"Be brief"}`},
		{"publish_agent", map[string]any{"agentId": testAgentID, "description": "v2"}, "POST", "/agent/v1/publish", "", `{"agentId":"` + testAgentID + `","description":"v2"}`},
		{"publish_agent", map[string]any{"agentId": testAgentID}, "POST", "/agent/v1/publish", "", `{"agentId":"` + testAgentID + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			cs, fake := connect(t, "secret", nil)
			text, isErr := callTool(t, cs, tt.tool, tt.args)
			if isErr {
				t.Fatalf("tool error: %s", text)
			}
			req := fake.last(t)
			if req.Method != tt.method || req.Path != tt.path || req.Query != tt.query {
				t.Errorf("got %s %s?%s, want %s %s?%s", req.Method, req.Path, req.Query, tt.method, tt.path, tt.query)
			}
			if req.Auth != "Bearer secret" {
				t.Errorf("Authorization = %q", req.Auth)
			}
			assertJSONEqual(t, req.Body, tt.wantBody)
		})
	}
}

func TestUpdateAgentSendsArraysAndClears(t *testing.T) {
	cs, fake := connect(t, "k", nil)
	args := map[string]any{
		"agentId":              testAgentID,
		"conversationStarters": []any{"One", "Two", "Three"},
		"actions":              []any{map[string]any{"actionId": "a1", "requiresConfirmation": false}, map[string]any{"actionId": "a2"}},
		"knowledgeFolderIds":   []any{},
		"description":          "",
		"clearEmoji":           true,
	}
	if text, isErr := callTool(t, cs, "update_agent", args); isErr {
		t.Fatalf("tool error: %s", text)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("update_agent must not read before writing, sent %d requests", len(fake.requests))
	}
	assertJSONEqual(t, fake.last(t).Body, `{"agentId":"`+testAgentID+`","conversationStarters":["One","Two","Three"],
		"actions":[{"actionId":"a1","requiresConfirmation":false},{"actionId":"a2"}],"knowledgeFolderIds":[],"description":"","emoji":null}`)
}

func TestUpdateAgentRejectsConflictsAndNoops(t *testing.T) {
	for name, args := range map[string]map[string]any{
		"emoji+clear": {"agentId": testAgentID, "emoji": "🤖", "clearEmoji": true},
		"nothing":     {"agentId": testAgentID},
		"publishOnly": {"agentId": testAgentID, "publish": true},
	} {
		t.Run(name, func(t *testing.T) {
			cs, fake := connect(t, "k", nil)
			if _, isErr := callTool(t, cs, "update_agent", args); !isErr {
				t.Fatal("expected tool error")
			}
			if len(fake.requests) != 0 {
				t.Fatal("request reached the API")
			}
		})
	}
}

func TestUpdateAgentWithPublish(t *testing.T) {
	cs, fake := connect(t, "k", map[string]fakeResponse{
		"PATCH /agent/v1/update": {200, `{"status":"success","agent":{"id":"x"}}`},
		"POST /agent/v1/publish": {200, `{"status":"success","version":{"version":3}}`},
	})
	text, isErr := callTool(t, cs, "update_agent", map[string]any{"agentId": testAgentID, "name": "B", "publish": true, "publishDescription": "rename"})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	if len(fake.requests) != 2 || fake.requests[0].Method != "PATCH" {
		t.Fatalf("want PATCH then publish, got %+v", fake.requests)
	}
	assertJSONEqual(t, fake.requests[0].Body, `{"agentId":"`+testAgentID+`","name":"B"}`)
	assertJSONEqual(t, fake.requests[1].Body, `{"agentId":"`+testAgentID+`","description":"rename"}`)
	assertJSONEqual(t, []byte(text), `{"draft":{"status":"success","agent":{"id":"x"}},"publish":{"status":"success","version":{"version":3}}}`)
}

func TestPublishFailureAfterWriteSaysDraftSaved(t *testing.T) {
	cs, _ := connect(t, "k", map[string]fakeResponse{
		"POST /agent/v1/publish": {http.StatusConflict, `{"message":"no changes"}`},
	})
	text, isErr := callTool(t, cs, "update_agent", map[string]any{"agentId": testAgentID, "name": "B", "publish": true})
	if !isErr || !strings.Contains(text, "draft") || !strings.Contains(text, "saved") || !strings.Contains(text, "no changes to publish") {
		t.Fatalf("got %v %q", isErr, text)
	}
}

func TestCreateAgentWithPublishUsesNewID(t *testing.T) {
	cs, fake := connect(t, "k", map[string]fakeResponse{
		"POST /agent/v1/create": {201, `{"status":"success","agent":{"id":"new-id","name":"Bot"}}`},
	})
	text, isErr := callTool(t, cs, "create_agent", map[string]any{"name": "Bot", "model": "m", "publish": true})
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	assertJSONEqual(t, fake.requests[0].Body, `{"name":"Bot","model":"m"}`)
	if req := fake.last(t); req.Path != "/agent/v1/publish" {
		t.Fatalf("last request %s %s", req.Method, req.Path)
	}
	assertJSONEqual(t, fake.last(t).Body, `{"agentId":"new-id"}`)
}

func TestAddAgentActionsMergesWithCurrentList(t *testing.T) {
	cs, fake := connect(t, "k", map[string]fakeResponse{"GET /agent/v1/get": {200, agentWithActions}})
	args := map[string]any{"agentId": testAgentID, "actions": []any{
		map[string]any{"actionId": "a3"},
		map[string]any{"actionId": "a2", "requiresConfirmation": true},
	}}
	if text, isErr := callTool(t, cs, "add_agent_actions", args); isErr {
		t.Fatalf("tool error: %s", text)
	}
	if got := fake.requests[0]; got.Method != "GET" || got.Query != "agentId="+testAgentID {
		t.Fatalf("first request %s %s?%s", got.Method, got.Path, got.Query)
	}
	req := fake.last(t)
	if req.Method != "PATCH" || req.Path != "/agent/v1/update" {
		t.Fatalf("got %s %s", req.Method, req.Path)
	}
	assertJSONEqual(t, req.Body, `{"agentId":"`+testAgentID+`","actions":[
		{"actionId":"a1","requiresConfirmation":true,"connectionId":"c1"},
		{"actionId":"a2","requiresConfirmation":true},
		{"actionId":"a3"}]}`)
}

func TestRemoveAgentActionsKeepsOthers(t *testing.T) {
	cs, fake := connect(t, "k", map[string]fakeResponse{"GET /agent/v1/get": {200, agentWithActions}})
	if text, isErr := callTool(t, cs, "remove_agent_actions", map[string]any{"agentId": testAgentID, "actionIds": []any{"a2"}}); isErr {
		t.Fatalf("tool error: %s", text)
	}
	assertJSONEqual(t, fake.last(t).Body, `{"agentId":"`+testAgentID+`","actions":[{"actionId":"a1","requiresConfirmation":true,"connectionId":"c1"}]}`)
}

func TestRemoveLastAgentActionSendsEmptyList(t *testing.T) {
	cs, fake := connect(t, "k", map[string]fakeResponse{"GET /agent/v1/get": {200, agentWithActions}})
	if text, isErr := callTool(t, cs, "remove_agent_actions", map[string]any{"agentId": testAgentID, "actionIds": []any{"a1", "a2"}}); isErr {
		t.Fatalf("tool error: %s", text)
	}
	assertJSONEqual(t, fake.last(t).Body, `{"agentId":"`+testAgentID+`","actions":[]}`)
}

func TestRemoveUnknownAgentActionWritesNothing(t *testing.T) {
	cs, fake := connect(t, "k", map[string]fakeResponse{"GET /agent/v1/get": {200, agentWithActions}})
	text, isErr := callTool(t, cs, "remove_agent_actions", map[string]any{"agentId": testAgentID, "actionIds": []any{"a1", "nope"}})
	if !isErr || !strings.Contains(text, "nope") {
		t.Fatalf("got %v %q", isErr, text)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("expected only the read, got %d requests", len(fake.requests))
	}
}

func TestAgentSchemaRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"create_agent", map[string]any{"name": "Bot"}},
		{"create_agent", map[string]any{"name": strings.Repeat("x", 81), "model": "m"}},
		{"update_agent", map[string]any{"agentId": "a", "creativity": 1.5}},
		{"update_agent", map[string]any{"agentId": "a", "inputType": "CHAT"}},
		{"update_agent", map[string]any{"agentId": "a", "inputFields": []any{map[string]any{"slug": "s", "label": "l", "type": "PASSWORD", "order": 0}}}},
		{"update_agent", map[string]any{"agentId": "a", "conversationStarters": []any{""}}},
		{"add_agent_actions", map[string]any{"agentId": "a", "actions": []any{}}},
		{"remove_agent_actions", map[string]any{"agentId": "a", "actionIds": []any{}}},
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

func TestAgentAuthErrorsNameAgentScope(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		cs, _ := connect(t, "k", map[string]fakeResponse{"GET /agent/v1/get": {status, `{"message":"denied"}`}})
		text, isErr := callTool(t, cs, "get_agent", map[string]any{"agentId": testAgentID})
		if !isErr || !strings.Contains(text, "Agent API scope") || !strings.Contains(text, "denied") {
			t.Errorf("%d: got %v %q", status, isErr, text)
		}
	}
}

func TestDisableAgent(t *testing.T) {
	cs, fake := connect(t, "k", nil)
	if text, isErr := callTool(t, cs, "disable_agent", map[string]any{"agentId": testAgentID, "disabled": false}); isErr {
		t.Fatalf("tool error: %s", text)
	}
	req := fake.last(t)
	if req.Method != "PATCH" || req.Path != "/agent/v1/disable" {
		t.Errorf("got %s %s", req.Method, req.Path)
	}
	assertJSONEqual(t, req.Body, `{"agentId":"`+testAgentID+`","disabled":false}`)
}

func TestChatWithAgent(t *testing.T) {
	cs, fake := connect(t, "k", map[string]fakeResponse{
		"POST /agent/v1/chat/completions": {http.StatusOK, `{"messages":[{"id":"r1","role":"assistant","content":"Hi"}]}`},
	})
	args := map[string]any{
		"agentId": testAgentID,
		"messages": []any{
			map[string]any{"role": "user", "parts": []any{map[string]any{"type": "text", "text": "Hello"}}},
			map[string]any{"id": "m2", "role": "user", "parts": []any{map[string]any{"type": "text", "text": "Summarize"}},
				"metadata": map[string]any{"attachments": []any{testAgentID}}},
		},
		"output":   map[string]any{"type": "enum", "enum": []any{"yes", "no"}},
		"maxSteps": 3,
	}
	text, isErr := callTool(t, cs, "chat_with_agent", args)
	if isErr || !strings.Contains(text, `"content":"Hi"`) {
		t.Fatalf("got %v %s", isErr, text)
	}
	var sent struct {
		Messages []map[string]any `json:"messages"`
	}
	body := fake.last(t).Body
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	id, _ := sent.Messages[0]["id"].(string)
	if !strings.HasPrefix(id, "msg-") || len(id) != 20 {
		t.Errorf("generated id %q", id)
	}
	assertJSONEqual(t, []byte(strings.Replace(string(body), id, "GEN", 1)), `{"agentId":"`+testAgentID+`","stream":false,"maxSteps":3,
		"output":{"type":"enum","enum":["yes","no"]},
		"messages":[{"id":"GEN","role":"user","parts":[{"type":"text","text":"Hello"}]},
			{"id":"m2","role":"user","parts":[{"type":"text","text":"Summarize"}],"metadata":{"attachments":["`+testAgentID+`"]}}]}`)
}

func TestChatWithAgentRejectsInvalidInput(t *testing.T) {
	text := []any{map[string]any{"type": "text", "text": "Hi"}}
	for name, args := range map[string]map[string]any{
		"no messages":   {"agentId": testAgentID, "messages": []any{}},
		"bad role":      {"agentId": testAgentID, "messages": []any{map[string]any{"role": "tool", "parts": text}}},
		"untyped part":  {"agentId": testAgentID, "messages": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Hi"}}}}},
		"null messages": {"agentId": testAgentID, "messages": nil},
		"null parts":    {"agentId": testAgentID, "messages": []any{map[string]any{"role": "user", "parts": nil}}},
		"output type":   {"agentId": testAgentID, "messages": []any{map[string]any{"role": "user", "parts": text}}, "output": map[string]any{"type": "string"}},
		"image format":  {"agentId": testAgentID, "messages": []any{map[string]any{"role": "user", "parts": text}}, "imageResponseFormat": "png"},
		"maxSteps":      {"agentId": testAgentID, "messages": []any{map[string]any{"role": "user", "parts": text}}, "maxSteps": 21},
		"bad agentId":   {"agentId": "x", "messages": []any{map[string]any{"role": "user", "parts": text}}},
	} {
		t.Run(name, func(t *testing.T) {
			cs, fake := connect(t, "k", nil)
			if _, isErr := callTool(t, cs, "chat_with_agent", args); !isErr {
				t.Fatal("expected validation error")
			}
			if len(fake.requests) != 0 {
				t.Fatal("invalid input reached the API")
			}
		})
	}
}

// A follow-up resends the earlier assistant reply with its reasoning and tool
// parts, which must pass through unchanged.
func TestChatWithAgentResendsHistory(t *testing.T) {
	cs, fake := connect(t, "k", nil)
	msgs := []any{
		map[string]any{"id": "s", "role": "system", "parts": []any{map[string]any{"type": "text", "text": "Be brief"}}},
		map[string]any{"id": "u1", "role": "user", "parts": []any{map[string]any{"type": "file", "mediaType": "application/pdf", "url": "https://example.com/a.pdf", "filename": "a.pdf"}}},
		map[string]any{"id": "a1", "role": "assistant", "parts": []any{
			map[string]any{"type": "reasoning", "text": "Reading"},
			map[string]any{"type": "tool-search", "toolCallId": "c1", "state": "output-available", "input": map[string]any{"q": "x"}, "output": map[string]any{"n": 1}},
			map[string]any{"type": "text", "text": "Done"}}},
		map[string]any{"id": "u2", "role": "user", "parts": []any{map[string]any{"type": "text", "text": "More"}}},
	}
	if text, isErr := callTool(t, cs, "chat_with_agent", map[string]any{"agentId": testAgentID, "messages": msgs, "imageResponseFormat": "url"}); isErr {
		t.Fatalf("tool error: %s", text)
	}
	want, _ := json.Marshal(map[string]any{"agentId": testAgentID, "stream": false, "imageResponseFormat": "url", "messages": msgs})
	assertJSONEqual(t, fake.last(t).Body, string(want))
}

func TestChatWithAgentTimeoutHint(t *testing.T) {
	cs, _ := connect(t, "k", map[string]fakeResponse{"POST /agent/v1/chat/completions": {524, ``}})
	text, isErr := callTool(t, cs, "chat_with_agent", map[string]any{"agentId": testAgentID,
		"messages": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"type": "text", "text": "Hi"}}}}})
	if !isErr || !strings.Contains(text, "100-second") {
		t.Errorf("got %v %q", isErr, text)
	}
}
