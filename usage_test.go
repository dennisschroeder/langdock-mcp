package main

import (
	"net/http"
	"strings"
	"testing"
)

var (
	usageFrom = map[string]any{"date": "2024-01-01T00:00:00.000", "timezone": "Europe/Berlin"}
	usageTo   = map[string]any{"date": "2024-01-31T23:59:59.999", "timezone": "UTC"}
)

const usageRange = `"from":{"date":"2024-01-01T00:00:00.000","timezone":"Europe/Berlin"},"to":{"date":"2024-01-31T23:59:59.999","timezone":"UTC"}`

func usageArgs(dataType string, extra map[string]any) map[string]any {
	args := map[string]any{"dataType": dataType, "from": usageFrom, "to": usageTo}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

func TestExportUsageMapsToEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		path     string
		wantBody string
	}{
		{"users", usageArgs("users", nil), "/export/users/json", `{` + usageRange + `}`},
		{"agents by model", usageArgs("agents", map[string]any{"group_by": "model"}), "/export/agents/json", `{` + usageRange + `,"group_by":"model"}`},
		{"api-keys csv", usageArgs("api-keys", map[string]any{"format": "csv"}), "/export/api-keys/csv", `{` + usageRange + `}`},
		{"projects", usageArgs("projects", nil), "/export/projects/json", `{` + usageRange + `}`},
		{"models by deployment", usageArgs("models", map[string]any{"group_by": "deployment"}), "/export/models/json", `{` + usageRange + `,"group_by":"deployment"}`},
		{"workflows", usageArgs("workflows", map[string]any{"format": "json"}), "/export/workflows/json", `{` + usageRange + `}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, fake := connect(t, "secret", nil)
			text, isErr := callTool(t, cs, "export_usage", tt.args)
			if isErr {
				t.Fatalf("tool error: %s", text)
			}
			req := fake.last(t)
			if req.Method != "POST" || req.Path != tt.path {
				t.Errorf("got %s %s, want POST %s", req.Method, req.Path, tt.path)
			}
			if req.Auth != "Bearer secret" {
				t.Errorf("Authorization = %q", req.Auth)
			}
			assertJSONEqual(t, req.Body, tt.wantBody)
		})
	}
}

func TestExportUsageRejectsInvalidInput(t *testing.T) {
	cases := map[string]map[string]any{
		"unknown dataType":          usageArgs("chats", nil),
		"unknown format":            usageArgs("users", map[string]any{"format": "xlsx"}),
		"missing to":                {"dataType": "users", "from": usageFrom},
		"missing timezone":          usageArgs("users", map[string]any{"to": map[string]any{"date": "2024-01-31T23:59:59.999"}}),
		"group_by for projects":     usageArgs("projects", map[string]any{"group_by": "model"}),
		"group_by for workflows":    usageArgs("workflows", map[string]any{"group_by": "model"}),
		"source for users":          usageArgs("users", map[string]any{"group_by": "source"}),
		"model for models":          usageArgs("models", map[string]any{"group_by": "model"}),
		"unknown group_by for user": usageArgs("users", map[string]any{"group_by": "day"}),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			cs, fake := connect(t, "k", nil)
			if _, isErr := callTool(t, cs, "export_usage", args); !isErr {
				t.Fatal("expected validation error")
			}
			if len(fake.requests) != 0 {
				t.Fatal("invalid input reached the API")
			}
		})
	}
}

func TestExportUsageErrorHints(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "shorter period"},
		{http.StatusUnauthorized, "USAGE_EXPORT_API scope"},
		{http.StatusForbidden, "USAGE_EXPORT_API scope"},
		{http.StatusNotFound, "no usage data"},
	}
	for _, tt := range tests {
		cs, _ := connect(t, "k", map[string]fakeResponse{"POST /export/users/json": {tt.status, `{"message":"boom"}`}})
		text, isErr := callTool(t, cs, "export_usage", usageArgs("users", nil))
		if !isErr || !strings.Contains(text, tt.want) || !strings.Contains(text, "boom") {
			t.Errorf("%d: got %v %q", tt.status, isErr, text)
		}
	}
}

func TestExportUsageTooLargeSuggestsCSV(t *testing.T) {
	huge := `{"success":true,"data":["` + strings.Repeat("x", maxResponseSize) + `"]}`
	cs, _ := connect(t, "k", map[string]fakeResponse{"POST /export/users/json": {http.StatusOK, huge}})
	text, isErr := callTool(t, cs, "export_usage", usageArgs("users", nil))
	if !isErr || !strings.Contains(text, "format csv") {
		t.Errorf("got %v %q", isErr, text)
	}
}

func TestExportUsageReturnsBodyVerbatim(t *testing.T) {
	body := `{"success":true,"data":[{"period_start":"2024-01-01","org_id":"w"}],"metadata":{"dataType":"users","recordCount":1}}`
	cs, _ := connect(t, "k", map[string]fakeResponse{"POST /export/users/json": {http.StatusOK, body}})
	text, isErr := callTool(t, cs, "export_usage", usageArgs("users", nil))
	if isErr || text != body {
		t.Errorf("got %v %q", isErr, text)
	}
}

func TestFamilyOfUsage(t *testing.T) {
	for path, want := range map[string]apiFamily{
		"/export/users/json":   usageAPI,
		"/export/api-keys/csv": usageAPI,
		"/exporter":            integrationsAPI,
	} {
		if got := familyOf(path); got != want {
			t.Errorf("familyOf(%q) = %d, want %d", path, got, want)
		}
	}
}
