package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestUserToolsMapToEndpoints(t *testing.T) {
	tests := []struct {
		tool     string
		args     map[string]any
		path     string
		wantBody string
	}{
		{"invite_users",
			map[string]any{"users": []any{map[string]any{"email": "a@example.com"}, map[string]any{"email": "b@example.com", "role": "editor"}}},
			"/user-management/v1/invite",
			`{"users":[{"email":"a@example.com"},{"email":"b@example.com","role":"editor"}]}`},
		{"update_user_role", map[string]any{"email": "a@example.com", "role": "admin"}, "/user-management/v1/update-user-role", `{"email":"a@example.com","role":"admin"}`},
		{"deactivate_user", map[string]any{"email": "a@example.com"}, "/user-management/v1/deactivate-user", `{"email":"a@example.com"}`},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			cs, fake := connect(t, "secret", nil)
			text, isErr := callTool(t, cs, tt.tool, tt.args)
			if isErr {
				t.Fatalf("tool error: %s", text)
			}
			req := fake.last(t)
			if req.Method != "POST" || req.Path != tt.path || req.Query != "" {
				t.Errorf("got %s %s?%s, want POST %s", req.Method, req.Path, req.Query, tt.path)
			}
			if req.Auth != "Bearer secret" {
				t.Errorf("Authorization = %q", req.Auth)
			}
			assertJSONEqual(t, req.Body, tt.wantBody)
		})
	}
}

func TestUserSchemaRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"no users", "invite_users", map[string]any{"users": []any{}}},
		{"bad invite role", "invite_users", map[string]any{"users": []any{map[string]any{"email": "a@example.com", "role": "owner"}}}},
		{"empty invite email", "invite_users", map[string]any{"users": []any{map[string]any{"email": ""}}}},
		{"missing role", "update_user_role", map[string]any{"email": "a@example.com"}},
		{"uppercase role", "update_user_role", map[string]any{"email": "a@example.com", "role": "ADMIN"}},
		{"empty email", "deactivate_user", map[string]any{"email": ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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

func TestUserErrorHints(t *testing.T) {
	cs, _ := connect(t, "k", map[string]fakeResponse{
		"POST /user-management/v1/update-user-role": {http.StatusBadRequest, `{"message":"last admin"}`},
		"POST /user-management/v1/deactivate-user":  {http.StatusNotFound, ``},
		"POST /user-management/v1/invite":           {http.StatusForbidden, ``},
	})
	text, isErr := callTool(t, cs, "update_user_role", map[string]any{"email": "a@example.com", "role": "member"})
	if !isErr || !strings.Contains(text, "400") || !strings.Contains(text, "active admin") || !strings.Contains(text, "last admin") {
		t.Errorf("400: got %v %q", isErr, text)
	}
	text, isErr = callTool(t, cs, "deactivate_user", map[string]any{"email": "a@example.com"})
	if !isErr || !strings.Contains(text, "no active human workspace member") {
		t.Errorf("404: got %v %q", isErr, text)
	}
	text, isErr = callTool(t, cs, "invite_users", map[string]any{"users": []any{map[string]any{"email": "a@example.com"}}})
	if !isErr || !strings.Contains(text, "USER_MANAGEMENT_API") || strings.Contains(text, "INTEGRATION_API") {
		t.Errorf("403: got %v %q", isErr, text)
	}
}

func TestUserFamilyOf(t *testing.T) {
	for path, want := range map[string]apiFamily{
		"/user-management/v1/invite":          usersAPI,
		"/user-management/v1/deactivate-user": usersAPI,
		"/user-managementx":                   integrationsAPI,
	} {
		if got := familyOf(path); got != want {
			t.Errorf("familyOf(%q) = %d, want %d", path, got, want)
		}
	}
}

func TestUserToolAnnotations(t *testing.T) {
	cs, _ := connect(t, "k", nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"invite_users": false, "update_user_role": true, "deactivate_user": true}
	for _, tool := range res.Tools {
		destructive, ok := want[tool.Name]
		if !ok {
			continue
		}
		delete(want, tool.Name)
		a := tool.Annotations
		if a == nil || a.DestructiveHint == nil || *a.DestructiveHint != destructive || a.ReadOnlyHint {
			t.Errorf("%s: annotations %+v, want destructiveHint %v", tool.Name, a, destructive)
		}
		if !strings.Contains(tool.Description, "Side effect") {
			t.Errorf("%s: description does not state its side effects", tool.Name)
		}
	}
	for name := range want {
		t.Errorf("%s missing from tools/list", name)
	}
}
