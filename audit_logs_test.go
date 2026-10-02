package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const (
	testWorkspaceID = "55555555-5555-5555-5555-555555555555"
	testActorID     = "66666666-6666-6666-6666-666666666666"
	testCursor      = "77777777-7777-7777-7777-777777777777"
)

func TestListAuditLogsMapsToEndpoint(t *testing.T) {
	tests := []struct {
		name  string
		args  map[string]any
		query string
	}{
		{"no filters", map[string]any{"workspace_id": testWorkspaceID}, ""},
		{"all filters",
			map[string]any{
				"workspace_id": testWorkspaceID, "from": "2026-09-01T00:00:00Z", "to": "2026-10-01T00:00:00Z",
				"entity_type": "User", "actor_id": testActorID, "limit": 10, "cursor": testCursor,
			},
			"actor_id=" + testActorID + "&cursor=" + testCursor + "&entity_type=User&from=2026-09-01T00%3A00%3A00Z&limit=10&to=2026-10-01T00%3A00%3A00Z"},
		{"entity type outside the documented examples", map[string]any{"workspace_id": testWorkspaceID, "entity_type": "ApiKey"}, "entity_type=ApiKey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, fake := connect(t, "secret", nil)
			text, isErr := callTool(t, cs, "list_audit_logs", tt.args)
			if isErr {
				t.Fatalf("tool error: %s", text)
			}
			req := fake.last(t)
			if req.Method != "GET" || req.Path != "/audit-logs/"+testWorkspaceID || req.Query != tt.query {
				t.Errorf("got %s %s?%s, want GET /audit-logs/%s?%s", req.Method, req.Path, req.Query, testWorkspaceID, tt.query)
			}
			if req.Auth != "Bearer secret" {
				t.Errorf("Authorization = %q", req.Auth)
			}
		})
	}
}

func TestListAuditLogsRejectsInvalidInput(t *testing.T) {
	for name, args := range map[string]map[string]any{
		"missing workspace": {},
		"empty workspace":   {"workspace_id": ""},
		"limit 0":           {"workspace_id": testWorkspaceID, "limit": 0},
		"limit 51":          {"workspace_id": testWorkspaceID, "limit": 51},
	} {
		t.Run(name, func(t *testing.T) {
			cs, fake := connect(t, "k", nil)
			if text, isErr := callTool(t, cs, "list_audit_logs", args); !isErr {
				t.Fatalf("expected error, got %q", text)
			}
			if len(fake.requests) != 0 {
				t.Fatal("invalid input reached the API")
			}
		})
	}
}

func TestAuditLogErrorHints(t *testing.T) {
	cs, _ := connect(t, "k", map[string]fakeResponse{
		"GET /audit-logs/" + testWorkspaceID: {http.StatusForbidden, `{"message":"forbidden"}`},
	})
	text, isErr := callTool(t, cs, "list_audit_logs", map[string]any{"workspace_id": testWorkspaceID})
	if !isErr || !strings.Contains(text, "403") || !strings.Contains(text, "AUDIT_LOG_API") || !strings.Contains(text, "forbidden") || strings.Contains(text, "INTEGRATION_API") {
		t.Errorf("403: got %v %q", isErr, text)
	}
}

func TestAuditLogsFamily(t *testing.T) {
	for path, want := range map[string]apiFamily{
		"/audit-logs/w":         auditLogsAPI,
		"/audit-logs/w?limit=1": auditLogsAPI,
		"/audit-logsx/w":        integrationsAPI,
	} {
		if got := familyOf(path); got != want {
			t.Errorf("familyOf(%q) = %d, want %d", path, got, want)
		}
	}
}

func TestAuditLogToolsListed(t *testing.T) {
	cs, _ := connect(t, "k", nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name == "list_audit_logs" {
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Error("list_audit_logs must be read-only")
			}
			return
		}
	}
	t.Error("list_audit_logs missing from tools/list")
}
