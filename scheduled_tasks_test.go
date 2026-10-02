package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const testTaskID = "66666666-6666-6666-6666-666666666666"

func TestScheduledTaskToolsMapToEndpoints(t *testing.T) {
	task := "/automations/v1/" + testTaskID
	tests := []struct {
		tool     string
		args     map[string]any
		method   string
		path     string
		query    string
		wantBody string
	}{
		{"list_scheduled_tasks", map[string]any{}, "GET", "/automations/v1", "", ""},
		{"list_scheduled_tasks", map[string]any{"limit": 250, "cursor": testTaskID}, "GET", "/automations/v1", "cursor=" + testTaskID + "&limit=250", ""},
		{"get_scheduled_task", map[string]any{"automationId": testTaskID}, "GET", task, "", ""},
		{"create_scheduled_task", map[string]any{"name": "Brief", "prompt": "Summarize", "frequency": "WEEKLY", "timeOfDay": "09:00", "dayOfWeek": 0, "timezone": "Europe/Berlin"}, "POST", "/automations/v1", "",
			`{"name":"Brief","prompt":"Summarize","frequency":"WEEKLY","timeOfDay":"09:00","dayOfWeek":0,"timezone":"Europe/Berlin"}`},
		{"update_scheduled_task", map[string]any{"automationId": testTaskID, "prompt": "New"}, "PATCH", task, "", `{"prompt":"New"}`},
		{"update_scheduled_task", map[string]any{"automationId": testTaskID, "taggedSkillSlugs": []any{}, "clearAssistantId": true}, "PATCH", task, "", `{"taggedSkillSlugs":[],"assistantId":null}`},
		{"delete_scheduled_task", map[string]any{"automationId": testTaskID}, "DELETE", task, "", ""},
		{"pause_scheduled_task", map[string]any{"automationId": testTaskID}, "POST", task + "/pause", "", ""},
		{"resume_scheduled_task", map[string]any{"automationId": testTaskID}, "POST", task + "/resume", "", ""},
		{"run_scheduled_task", map[string]any{"automationId": testTaskID}, "POST", task + "/run", "", ""},
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
			if tt.wantBody != "" && req.ContentType != "application/json" {
				t.Errorf("Content-Type = %q", req.ContentType)
			}
			assertJSONEqual(t, req.Body, tt.wantBody)
		})
	}
}

func TestScheduledTaskSchemaRejectsInvalidInput(t *testing.T) {
	valid := func(extra map[string]any) map[string]any {
		args := map[string]any{"name": "n", "prompt": "p", "frequency": "DAILY", "timeOfDay": "08:30"}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}
	cases := map[string]struct {
		tool string
		args map[string]any
	}{
		"missing frequency": {"create_scheduled_task", map[string]any{"name": "n", "prompt": "p"}},
		"unknown frequency": {"create_scheduled_task", valid(map[string]any{"frequency": "HOURLY"})},
		"empty name":        {"create_scheduled_task", valid(map[string]any{"name": ""})},
		"long name":         {"create_scheduled_task", valid(map[string]any{"name": strings.Repeat("x", 201)})},
		"bad time":          {"create_scheduled_task", valid(map[string]any{"timeOfDay": "24:00"})},
		"dayOfWeek 7":       {"create_scheduled_task", valid(map[string]any{"frequency": "WEEKLY", "dayOfWeek": 7})},
		"dayOfMonth 0":      {"create_scheduled_task", valid(map[string]any{"frequency": "MONTHLY", "dayOfMonth": 0})},
		"daysOfWeek item":   {"create_scheduled_task", valid(map[string]any{"frequency": "SELECTED_WEEKDAYS", "daysOfWeek": []any{1, 8}})},
		"modelMode":         {"create_scheduled_task", valid(map[string]any{"modelMode": "FIXED"})},
		"too many tags":     {"create_scheduled_task", valid(map[string]any{"taggedWorkflowIds": make([]any, 21)})},
		"long skill slug":   {"update_scheduled_task", map[string]any{"automationId": testTaskID, "taggedSkillSlugs": []any{strings.Repeat("s", 101)}}},
		"empty id":          {"pause_scheduled_task", map[string]any{"automationId": ""}},
		"limit 251":         {"list_scheduled_tasks", map[string]any{"limit": 251}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
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

func TestUpdateScheduledTaskRejectsBeforeRequest(t *testing.T) {
	for name, args := range map[string]map[string]any{
		"empty":         {"automationId": testTaskID},
		"set and clear": {"automationId": testTaskID, "assistantId": "a", "clearAssistantId": true},
		"tagged both":   {"automationId": testTaskID, "taggedAssistantId": "a", "clearTaggedAssistantId": true},
	} {
		t.Run(name, func(t *testing.T) {
			cs, fake := connect(t, "k", nil)
			if _, isErr := callTool(t, cs, "update_scheduled_task", args); !isErr {
				t.Fatal("expected tool error")
			}
			if len(fake.requests) != 0 {
				t.Fatal("request reached the API")
			}
		})
	}
}

func TestScheduledTaskErrorHints(t *testing.T) {
	cs, _ := connect(t, "k", map[string]fakeResponse{
		"GET /automations/v1":                         {http.StatusForbidden, `{"message":"denied"}`},
		"POST /automations/v1":                        {http.StatusBadRequest, `{"message":"AUTOMATION_LIMIT_REACHED"}`},
		"POST /automations/v1/" + testTaskID + "/run": {http.StatusNotFound, `{"message":"AUTOMATION_NOT_FOUND"}`},
	})
	text, isErr := callTool(t, cs, "list_scheduled_tasks", map[string]any{})
	if !isErr || !strings.Contains(text, "403") || !strings.Contains(text, "AUTOMATION_API") || !strings.Contains(text, "denied") {
		t.Errorf("403: got %v %q", isErr, text)
	}
	text, isErr = callTool(t, cs, "create_scheduled_task", map[string]any{"name": "n", "prompt": "p", "frequency": "MANUAL"})
	if !isErr || !strings.Contains(text, "10 tasks per owner") {
		t.Errorf("400: got %v %q", isErr, text)
	}
	text, isErr = callTool(t, cs, "run_scheduled_task", map[string]any{"automationId": testTaskID})
	if !isErr || !strings.Contains(text, "scheduled task not found") || strings.Contains(text, "integration") {
		t.Errorf("404: got %v %q", isErr, text)
	}
}

func TestFamilyOfScheduledTasks(t *testing.T) {
	for path, want := range map[string]apiFamily{
		"/automations/v1":                 scheduledTasksAPI,
		"/automations/v1?limit=1":         scheduledTasksAPI,
		"/automations/v1/x/run":           scheduledTasksAPI,
		"/automations/v10":                integrationsAPI,
		"/integrations/v1/automations/v1": integrationsAPI,
	} {
		if got := familyOf(path); got != want {
			t.Errorf("familyOf(%q) = %d, want %d", path, got, want)
		}
	}
}

func TestScheduledTaskToolsListed(t *testing.T) {
	cs, _ := connect(t, "k", nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, tool := range res.Tools {
		listed[tool.Name] = true
	}
	for _, name := range []string{
		"list_scheduled_tasks", "create_scheduled_task", "get_scheduled_task", "update_scheduled_task",
		"delete_scheduled_task", "pause_scheduled_task", "resume_scheduled_task", "run_scheduled_task",
	} {
		if !listed[name] {
			t.Errorf("%s missing from tools/list", name)
		}
	}
}
