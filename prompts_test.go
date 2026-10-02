package main

import (
	"net/http"
	"strings"
	"testing"
)

const (
	testPromptID       = "66666666-6666-6666-6666-666666666666"
	testPromptFolderID = "77777777-7777-7777-7777-777777777777"
	testGroupID        = "88888888-8888-8888-8888-888888888888"
)

func TestPromptToolsMapToEndpoints(t *testing.T) {
	p := "/prompts/v1/" + testPromptID
	f := "/prompts/v1/folders/" + testPromptFolderID
	tests := []struct {
		tool     string
		args     map[string]any
		method   string
		path     string
		query    string
		wantBody string
	}{
		{"list_prompts", map[string]any{}, "GET", "/prompts/v1", "", ""},
		{"list_prompts",
			map[string]any{"limit": 25, "cursor": "c", "query": "support", "promptFolderId": testPromptFolderID, "sharedWithWorkspace": false},
			"GET", "/prompts/v1", "cursor=c&limit=25&promptFolderId=" + testPromptFolderID + "&query=support&sharedWithWorkspace=false", ""},
		{"create_prompt", map[string]any{"title": "Support Reply", "prompt": "Write a reply."}, "POST", "/prompts/v1", "", `{"title":"Support Reply","prompt":"Write a reply."}`},
		{"create_prompt", map[string]any{"title": "Hi", "prompt": "Hi", "sharedWithWorkspace": true}, "POST", "/prompts/v1", "", `{"title":"Hi","prompt":"Hi","sharedWithWorkspace":true}`},
		{"get_prompt", map[string]any{"promptId": testPromptID}, "GET", p, "", ""},
		{"update_prompt", map[string]any{"promptId": testPromptID, "prompt": "New text"}, "PATCH", p, "", `{"prompt":"New text"}`},
		{"update_prompt", map[string]any{"promptId": testPromptID, "sharedWithWorkspace": false, "promptFolderId": testPromptFolderID}, "PATCH", p, "", `{"sharedWithWorkspace":false,"promptFolderId":"` + testPromptFolderID + `"}`},
		{"update_prompt", map[string]any{"promptId": testPromptID, "clearPromptFolderId": true}, "PATCH", p, "", `{"promptFolderId":null}`},
		{"delete_prompt", map[string]any{"promptId": testPromptID}, "DELETE", p, "", ""},
		{"list_prompt_folders", map[string]any{"query": "HR", "sharedWithWorkspace": true}, "GET", "/prompts/v1/folders", "query=HR&sharedWithWorkspace=true", ""},
		{"create_prompt_folder", map[string]any{"name": "HR", "sharedWithGroupId": testGroupID}, "POST", "/prompts/v1/folders", "", `{"name":"HR","sharedWithGroupId":"` + testGroupID + `"}`},
		{"get_prompt_folder", map[string]any{"folderId": testPromptFolderID}, "GET", f, "", ""},
		{"update_prompt_folder", map[string]any{"folderId": testPromptFolderID, "name": "People"}, "PATCH", f, "", `{"name":"People"}`},
		{"update_prompt_folder", map[string]any{"folderId": testPromptFolderID, "clearSharedWithGroupId": true, "sharedWithWorkspace": true}, "PATCH", f, "", `{"sharedWithGroupId":null,"sharedWithWorkspace":true}`},
		{"delete_prompt_folder", map[string]any{"folderId": testPromptFolderID}, "DELETE", f, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			cs, fake := connect(t, "secret", nil)
			text, isErr := callTool(t, cs, tt.tool, tt.args)
			if isErr {
				t.Fatalf("tool error: %s", text)
			}
			if len(fake.requests) != 1 {
				t.Fatalf("sent %d requests, want 1", len(fake.requests))
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

func TestUpdatePromptRejectsConflictsAndNoops(t *testing.T) {
	for name, c := range map[string]struct {
		tool string
		args map[string]any
	}{
		"prompt nothing":      {"update_prompt", map[string]any{"promptId": testPromptID}},
		"prompt folder+clear": {"update_prompt", map[string]any{"promptId": testPromptID, "promptFolderId": testPromptFolderID, "clearPromptFolderId": true}},
		"folder nothing":      {"update_prompt_folder", map[string]any{"folderId": testPromptFolderID}},
		"folder group+clear":  {"update_prompt_folder", map[string]any{"folderId": testPromptFolderID, "sharedWithGroupId": testGroupID, "clearSharedWithGroupId": true}},
	} {
		t.Run(name, func(t *testing.T) {
			cs, fake := connect(t, "k", nil)
			if _, isErr := callTool(t, cs, c.tool, c.args); !isErr {
				t.Fatal("expected tool error")
			}
			if len(fake.requests) != 0 {
				t.Fatal("request reached the API")
			}
		})
	}
}

func TestPromptSchemaRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"create_prompt", map[string]any{"title": "T"}},
		{"create_prompt", map[string]any{"title": "x", "prompt": "Hello"}},
		{"create_prompt", map[string]any{"title": strings.Repeat("x", 101), "prompt": "Hello"}},
		{"update_prompt", map[string]any{"promptId": testPromptID, "prompt": strings.Repeat("x", 120001)}},
		{"get_prompt", map[string]any{"promptId": "folders"}},
		{"delete_prompt", map[string]any{"promptId": ""}},
		{"delete_prompt_folder", map[string]any{"folderId": "../" + testPromptFolderID}},
		{"list_prompts", map[string]any{"limit": 251}},
		{"list_prompt_folders", map[string]any{"limit": 0}},
		{"create_prompt_folder", map[string]any{"name": strings.Repeat("x", 51)}},
		{"update_prompt_folder", map[string]any{"folderId": testPromptFolderID, "name": "x"}},
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

func TestPromptErrorHints(t *testing.T) {
	cs, _ := connect(t, "k", map[string]fakeResponse{
		"GET /prompts/v1": {http.StatusForbidden, `{"message":"denied"}`},
		"DELETE /prompts/v1/folders/" + testPromptFolderID: {http.StatusNotFound, ``},
	})
	text, isErr := callTool(t, cs, "list_prompts", map[string]any{})
	if !isErr || !strings.Contains(text, "PROMPT_API") || !strings.Contains(text, "sharePrompts") || !strings.Contains(text, "denied") {
		t.Errorf("403: got %v %q", isErr, text)
	}
	text, isErr = callTool(t, cs, "delete_prompt_folder", map[string]any{"folderId": testPromptFolderID})
	if !isErr || !strings.Contains(text, "prompt folder") {
		t.Errorf("404: got %v %q", isErr, text)
	}
}

func TestFamilyOfPrompts(t *testing.T) {
	for path, want := range map[string]apiFamily{
		"/prompts/v1":              promptsAPI,
		"/prompts/v1?limit=1":      promptsAPI,
		"/prompts/v1/folders/x":    promptsAPI,
		"/prompts/v10":             integrationsAPI,
		"/integrations/v1/prompts": integrationsAPI,
	} {
		if got := familyOf(path); got != want {
			t.Errorf("familyOf(%q) = %d, want %d", path, got, want)
		}
	}
}
