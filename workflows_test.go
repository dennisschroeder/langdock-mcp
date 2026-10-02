package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const testWorkflowID = "66666666-6666-6666-6666-666666666666"

func TestWorkflowToolsMapToEndpoints(t *testing.T) {
	id := testWorkflowID
	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		method   string
		path     string
		query    string
		wantBody string
	}{
		{"list", "list_workflows", map[string]any{}, "GET", "/workflows/v1/list", "", ""},
		{"listPage", "list_workflows", map[string]any{"limit": 250, "cursor": "c1"}, "GET", "/workflows/v1/list", "cursor=c1&limit=250", ""},
		{"get", "get_workflow", map[string]any{"workflowId": id}, "GET", "/workflows/v1/get", "workflowId=" + id, ""},
		{"create", "create_workflow",
			map[string]any{"name": "Sync", "timezone": "Europe/Berlin", "initialTriggerKind": "manual", "shareWith": map[string]any{"userIds": []any{"u1"}, "role": "editor"}, "limits": map[string]any{"perRunCostUsd": 5}},
			"POST", "/workflows/v1/create", "",
			`{"name":"Sync","timezone":"Europe/Berlin","initialTriggerKind":"manual","shareWith":{"userIds":["u1"],"role":"editor"},"limits":{"perRunCostUsd":5}}`},
		{"createGraph", "create_workflow",
			map[string]any{"name": "G", "nodes": []any{map[string]any{"id": "n1", "type": "trigger"}}, "edges": []any{}},
			"POST", "/workflows/v1/create", "",
			`{"name":"G","nodes":[{"id":"n1","type":"trigger"}],"edges":[]}`},
		{"updateMetadata", "update_workflow",
			map[string]any{"workflowId": id, "status": "INACTIVE", "description": ""},
			"PATCH", "/workflows/v1/update", "",
			`{"workflowId":"` + id + `","status":"INACTIVE","description":""}`},
		{"updateGraph", "update_workflow",
			map[string]any{"workflowId": id, "nodes": []any{}, "edges": []any{}},
			"PATCH", "/workflows/v1/update", "",
			`{"workflowId":"` + id + `","nodes":[],"edges":[]}`},
		{"updatePatch", "update_workflow",
			map[string]any{"workflowId": id, "patch": map[string]any{"ops": []any{}}},
			"PATCH", "/workflows/v1/update", "",
			`{"workflowId":"` + id + `","patch":{"ops":[]}}`},
		{"publish", "publish_workflow", map[string]any{"workflowId": id, "bumpType": "minor", "description": "v1.1"}, "POST", "/workflows/v1/publish", "", `{"workflowId":"` + id + `","bumpType":"minor","description":"v1.1"}`},
		{"delete", "delete_workflow", map[string]any{"workflowId": id}, "DELETE", "/workflows/v1/delete", "workflowId=" + id, ""},
		{"runs", "list_workflow_runs", map[string]any{"workflowId": id}, "GET", "/workflows/v1/runs", "workflowId=" + id, ""},
		{"runsFiltered", "list_workflow_runs",
			map[string]any{"workflowId": id, "limit": 10, "cursor": "r9", "runMode": "test", "version": "0", "status": "FAILED", "from": "2026-09-01", "to": "2026-09-30"},
			"GET", "/workflows/v1/runs", "cursor=r9&from=2026-09-01&limit=10&runMode=test&status=FAILED&to=2026-09-30&version=0&workflowId=" + id, ""},
		{"export", "export_workflow_runs", map[string]any{"workflowId": id, "from": "2026-08-01", "to": "2026-08-31T23:00:00+02:00"},
			"GET", "/workflows/" + id + "/runs", "from=2026-08-01&to=2026-08-31T23%3A00%3A00%2B02%3A00", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, fake := connect(t, "secret", nil)
			text, isErr := callTool(t, cs, tt.tool, tt.args)
			if isErr {
				t.Fatalf("tool error: %s", text)
			}
			if len(fake.requests) != 1 {
				t.Fatalf("want one request, got %d", len(fake.requests))
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

func TestWorkflowRemoveLimitsSendsNull(t *testing.T) {
	cs, fake := connect(t, "k", nil)
	args := map[string]any{"workflowId": testWorkflowID, "limits": map[string]any{"monthlyCostUsd": 50}, "removeLimits": []any{"perRunCostUsd", "maxExecutionsPerHour"}}
	if text, isErr := callTool(t, cs, "update_workflow", args); isErr {
		t.Fatalf("tool error: %s", text)
	}
	assertJSONEqual(t, fake.last(t).Body, `{"workflowId":"`+testWorkflowID+`","limits":{"monthlyCostUsd":50,"perRunCostUsd":null,"maxExecutionsPerHour":null}}`)

	args = map[string]any{"workflowId": testWorkflowID, "removeLimits": []any{"perRunCostUsd", "perRunCostUsd"}}
	if text, isErr := callTool(t, cs, "update_workflow", args); isErr {
		t.Fatalf("duplicate removeLimits: %s", text)
	}
	assertJSONEqual(t, fake.last(t).Body, `{"workflowId":"`+testWorkflowID+`","limits":{"perRunCostUsd":null}}`)
}

func TestWorkflowLocalValidation(t *testing.T) {
	id := testWorkflowID
	cases := map[string]struct {
		tool string
		args map[string]any
	}{
		"nodesWithoutEdges":    {"update_workflow", map[string]any{"workflowId": id, "nodes": []any{}}},
		"graphAndMetadata":     {"update_workflow", map[string]any{"workflowId": id, "name": "X", "nodes": []any{}, "edges": []any{}}},
		"patchAndMetadata":     {"update_workflow", map[string]any{"workflowId": id, "patch": map[string]any{"a": 1}, "limits": map[string]any{"perRunCostUsd": 2}}},
		"patchAndGraph":        {"update_workflow", map[string]any{"workflowId": id, "patch": map[string]any{"a": 1}, "nodes": []any{}, "edges": []any{}}},
		"nullLimit":            {"update_workflow", map[string]any{"workflowId": id, "limits": map[string]any{"perRunCostUsd": nil}}},
		"nullLimitOnCreate":    {"create_workflow", map[string]any{"name": "W", "limits": map[string]any{"monthlyCostUsd": nil}}},
		"nullDescription":      {"update_workflow", map[string]any{"workflowId": id, "description": nil}},
		"fractionalExecutions": {"update_workflow", map[string]any{"workflowId": id, "limits": map[string]any{"maxExecutionsPerHour": 1.5}}},
		"nothing":              {"update_workflow", map[string]any{"workflowId": id}},
		"setAndRemove":         {"update_workflow", map[string]any{"workflowId": id, "limits": map[string]any{"perRunCostUsd": 2}, "removeLimits": []any{"perRunCostUsd"}}},
		"triggerAndGraph":      {"create_workflow", map[string]any{"name": "W", "initialTriggerKind": "manual", "nodes": []any{}, "edges": []any{}}},
		"edgesWithoutNodes":    {"create_workflow", map[string]any{"name": "W", "edges": []any{}}},
		"fromWithoutTo":        {"list_workflow_runs", map[string]any{"workflowId": id, "from": "2026-09-01"}},
		"nameTooLong":          {"create_workflow", map[string]any{"name": strings.Repeat("x", 61)}},
		"unknownTrigger":       {"create_workflow", map[string]any{"name": "W", "initialTriggerKind": "email"}},
		"limitOutOfRange":      {"create_workflow", map[string]any{"name": "W", "limits": map[string]any{"perRunCostUsd": 101}}},
		"unknownLimit":         {"update_workflow", map[string]any{"workflowId": id, "removeLimits": []any{"dailyCostUsd"}}},
		"badStatus":            {"update_workflow", map[string]any{"workflowId": id, "status": "PAUSED"}},
		"publishWithoutBump":   {"publish_workflow", map[string]any{"workflowId": id}},
		"publishDescTooLong":   {"publish_workflow", map[string]any{"workflowId": id, "bumpType": "patch", "description": strings.Repeat("x", 256)}},
		"runsLimitTooHigh":     {"list_workflow_runs", map[string]any{"workflowId": id, "limit": 101}},
		"listLimitTooHigh":     {"list_workflows", map[string]any{"limit": 251}},
		"exportWithoutRange":   {"export_workflow_runs", map[string]any{"workflowId": id}},
		"exportEmptyID":        {"export_workflow_runs", map[string]any{"workflowId": "", "from": "2026-08-01", "to": "2026-08-31"}},
		"deleteWithoutID":      {"delete_workflow", map[string]any{}},
		"shareRoleUnknown":     {"create_workflow", map[string]any{"name": "W", "shareWith": map[string]any{"role": "owner"}}},
		"runModeDisagreesEnum": {"list_workflow_runs", map[string]any{"workflowId": id, "runMode": "prod"}},
	}
	for name, c := range cases {
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

func TestWorkflowErrorHints(t *testing.T) {
	cs, _ := connect(t, "k", map[string]fakeResponse{
		"DELETE /workflows/v1/delete":                {http.StatusForbidden, `{"message":"denied"}`},
		"GET /workflows/" + testWorkflowID + "/runs": {http.StatusBadRequest, `{"code":"WORKFLOW_RUN_EXPORT_LIMIT_EXCEEDED"}`},
	})
	text, isErr := callTool(t, cs, "delete_workflow", map[string]any{"workflowId": testWorkflowID})
	if !isErr || !strings.Contains(text, "403") || !strings.Contains(text, "WORKFLOW_DELETE_API") || !strings.Contains(text, "denied") {
		t.Errorf("403: got %v %q", isErr, text)
	}
	text, isErr = callTool(t, cs, "export_workflow_runs", map[string]any{"workflowId": testWorkflowID, "from": "2026-01-01", "to": "2026-12-31"})
	if !isErr || !strings.Contains(text, "narrow the date range") || !strings.Contains(text, "WORKFLOW_RUN_EXPORT_LIMIT_EXCEEDED") {
		t.Errorf("400: got %v %q", isErr, text)
	}
}

func TestWorkflowFamilies(t *testing.T) {
	for path, want := range map[string]apiFamily{
		"/workflows/v1/list":         workflowsAPI,
		"/workflows/v1/get?x=1":      workflowsAPI,
		"/workflows/abc/runs?from=1": workflowExportAPI,
		"/workflowsx":                integrationsAPI,
	} {
		if got := familyOf(path); got != want {
			t.Errorf("familyOf(%q) = %d, want %d", path, got, want)
		}
	}
}

func TestDeleteWorkflowSaysPermanent(t *testing.T) {
	cs, _ := connect(t, "k", nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{}
	for _, tool := range res.Tools {
		listed[tool.Name] = tool.Description
	}
	for _, name := range []string{
		"list_workflows", "get_workflow", "create_workflow", "update_workflow",
		"publish_workflow", "delete_workflow", "list_workflow_runs", "export_workflow_runs",
	} {
		if _, ok := listed[name]; !ok {
			t.Errorf("%s missing from tools/list", name)
		}
	}
	if d := listed["delete_workflow"]; !strings.Contains(d, "Permanently") || !strings.Contains(d, "cannot be undone") {
		t.Errorf("delete_workflow description must say the delete is permanent: %q", d)
	}
}
