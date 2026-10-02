package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	scheduledTaskFrequencies = []string{"MANUAL", "DAILY", "WEEKDAYS", "WEEKLY", "MONTHLY", "SELECTED_WEEKDAYS"}
	scheduledTaskModelModes  = []string{"UNSET", "EXPLICIT", "AUTO"}
)

type ListScheduledTasksInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"page size, 1-250, default 50"`
	Cursor string `json:"cursor,omitempty" jsonschema:"nextCursor from the previous page"`
}

type ScheduledTaskRef struct {
	AutomationID string `json:"automationId" jsonschema:"UUID of the scheduled task"`
}

// ScheduledTaskSettings are the writable fields shared by create and update.
// Pointers keep "omitted" distinct from zero values, because the update
// endpoint leaves omitted fields unchanged and replaces passed arrays.
type ScheduledTaskSettings struct {
	Name                     *string   `json:"name,omitempty" jsonschema:"1-200 characters"`
	Prompt                   *string   `json:"prompt,omitempty" jsonschema:"prompt sent on every run, 1-120000 characters"`
	Frequency                *string   `json:"frequency,omitempty" jsonschema:"MANUAL runs only via run_scheduled_task; every other frequency needs timeOfDay, WEEKLY also dayOfWeek, MONTHLY dayOfMonth, SELECTED_WEEKDAYS daysOfWeek"`
	TimeOfDay                *string   `json:"timeOfDay,omitempty" jsonschema:"24 hour HH:mm"`
	DayOfWeek                *int      `json:"dayOfWeek,omitempty" jsonschema:"for WEEKLY: 0 (Sunday) through 6 (Saturday)"`
	DayOfMonth               *int      `json:"dayOfMonth,omitempty" jsonschema:"for MONTHLY: 1 through 31; months without that day run on their last day"`
	DaysOfWeek               *[]int    `json:"daysOfWeek,omitempty" jsonschema:"for SELECTED_WEEKDAYS: days 0 (Sunday) through 6 (Saturday)"`
	Timezone                 *string   `json:"timezone,omitempty" jsonschema:"IANA name such as Europe/Berlin, max 64 characters; empty or omitted is stored as UTC on a scheduled frequency"`
	ModelMode                *string   `json:"modelMode,omitempty" jsonschema:"EXPLICIT requires modelId in the same request; passing only modelId implies EXPLICIT"`
	ModelID                  *string   `json:"modelId,omitempty" jsonschema:"model UUID"`
	AssistantID              *string   `json:"assistantId,omitempty" jsonschema:"UUID of the primary agent; must be available to the API key"`
	TaggedAssistantID        *string   `json:"taggedAssistantId,omitempty" jsonschema:"UUID of an extra @agent mention; dropped if the key cannot use it"`
	TaggedIntegrationIDs     *[]string `json:"taggedIntegrationIds,omitempty" jsonschema:"integration UUIDs, max 20; inaccessible ones are dropped; replaces the whole list"`
	TaggedKnowledgeFolderIDs *[]string `json:"taggedKnowledgeFolderIds,omitempty" jsonschema:"knowledge folder UUIDs, max 20; inaccessible ones are dropped; replaces the whole list"`
	TaggedWorkflowIDs        *[]string `json:"taggedWorkflowIds,omitempty" jsonschema:"workflow UUIDs, max 20; inaccessible ones are dropped; replaces the whole list"`
	TaggedSkillSlugs         *[]string `json:"taggedSkillSlugs,omitempty" jsonschema:"skill slugs, max 20, each max 100 characters; inaccessible ones are dropped; replaces the whole list"`
	AttachmentIDs            *[]string `json:"attachmentIds,omitempty" jsonschema:"attachment UUIDs owned by the API key's service account, max 20; replaces the whole list"`
}

type UpdateScheduledTaskInput struct {
	AutomationID string `json:"automationId" jsonschema:"UUID of the scheduled task"`
	ScheduledTaskSettings
	ClearAssistantID       bool `json:"clearAssistantId,omitempty" jsonschema:"set to remove the primary agent"`
	ClearTaggedAssistantID bool `json:"clearTaggedAssistantId,omitempty" jsonschema:"set to remove the extra @agent mention"`
}

func (s *Server) registerScheduledTasks() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)}
	additive := &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)}
	destructive := &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(true)}

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_scheduled_tasks",
		Description: "List the scheduled tasks owned by the API key, newest first, with their schedule, active state and runCount. Pass nextCursor as cursor for the next page; it is absent on the last page. Tasks of other keys or users are not visible.",
		Annotations: readOnly,
		InputSchema: schemaFor[ListScheduledTasksInput](func(sc *jsonschema.Schema) {
			at(sc, "limit").Minimum = ptr(1.0)
			at(sc, "limit").Maximum = ptr(250.0)
		}),
	}, s.listScheduledTasks)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "get_scheduled_task",
		Description: "Get one scheduled task owned by the API key, including runCount and lastRunAt.",
		Annotations: readOnly,
		InputSchema: schemaFor[ScheduledTaskRef](scheduledTaskID),
	}, s.getScheduledTask)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "create_scheduled_task",
		Description: "Create a scheduled task that sends prompt to an agent or model on a schedule. It starts active. Each API key's service account can own at most 10 tasks; the 11th fails with 400 AUTOMATION_LIMIT_REACHED.",
		Annotations: additive,
		InputSchema: schemaFor[ScheduledTaskSettings](func(sc *jsonschema.Schema) {
			scheduledTaskSchema(sc)
			sc.Required = append(sc.Required, "name", "prompt", "frequency")
		}),
	}, s.createScheduledTask)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_scheduled_task",
		Description: "Update a scheduled task. Only the fields you pass change. Array fields (daysOfWeek, tagged*, attachmentIds) replace the whole list when passed, and [] empties it. The schedule is validated after merging with the stored clock, so changing only frequency can reuse the stored timeOfDay. Switching to MANUAL clears the clock fields. clearAssistantId / clearTaggedAssistantId remove the agents.",
		Annotations: destructive,
		InputSchema: schemaFor[UpdateScheduledTaskInput](func(sc *jsonschema.Schema) {
			scheduledTaskSchema(sc)
			scheduledTaskID(sc)
		}),
	}, s.updateScheduledTask)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "delete_scheduled_task",
		Description: "Permanently delete a scheduled task. Cannot be undone.",
		Annotations: destructive,
		InputSchema: schemaFor[ScheduledTaskRef](scheduledTaskID),
	}, s.deleteScheduledTask)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "pause_scheduled_task",
		Description: "Pause a scheduled task so it no longer runs on its clock. run_scheduled_task still works on a paused task.",
		Annotations: destructive,
		InputSchema: schemaFor[ScheduledTaskRef](scheduledTaskID),
	}, s.pauseScheduledTask)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "resume_scheduled_task",
		Description: "Resume a paused scheduled task so it runs on its clock again (MANUAL tasks still only run via run_scheduled_task).",
		Annotations: destructive,
		InputSchema: schemaFor[ScheduledTaskRef](scheduledTaskID),
	}, s.resumeScheduledTask)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "run_scheduled_task",
		Description: "Enqueue one run of a scheduled task now, also when it is paused or MANUAL. Returns runId once the run is queued, without waiting for the conversation; conversationId is always null. Running does not resume a paused task.",
		Annotations: additive,
		InputSchema: schemaFor[ScheduledTaskRef](scheduledTaskID),
	}, s.runScheduledTask)
}

func scheduledTaskSchema(sc *jsonschema.Schema) {
	at(sc, "name").MinLength = ptr(1)
	maxLen(sc, 200, "name")
	at(sc, "prompt").MinLength = ptr(1)
	maxLen(sc, 120000, "prompt")
	enum(sc, scheduledTaskFrequencies, "frequency")
	at(sc, "timeOfDay").Pattern = `^([01][0-9]|2[0-3]):[0-5][0-9]$`
	at(sc, "dayOfWeek").Minimum = ptr(0.0)
	at(sc, "dayOfWeek").Maximum = ptr(6.0)
	at(sc, "dayOfMonth").Minimum = ptr(1.0)
	at(sc, "dayOfMonth").Maximum = ptr(31.0)
	at(sc, "daysOfWeek").MaxItems = ptr(7)
	at(sc, "daysOfWeek", "[]").Minimum = ptr(0.0)
	at(sc, "daysOfWeek", "[]").Maximum = ptr(6.0)
	maxLen(sc, 64, "timezone")
	enum(sc, scheduledTaskModelModes, "modelMode")
	for _, p := range []string{"taggedIntegrationIds", "taggedKnowledgeFolderIds", "taggedWorkflowIds", "taggedSkillSlugs", "attachmentIds"} {
		at(sc, p).MaxItems = ptr(20)
	}
	maxLen(sc, 100, "taggedSkillSlugs", "[]")
}

// scheduledTaskID requires a non-empty id, because an empty one addresses the
// collection instead, such as POST /automations/v1/ for pause.
func scheduledTaskID(sc *jsonschema.Schema) {
	at(sc, "automationId").MinLength = ptr(1)
}

func (s *Server) listScheduledTasks(ctx context.Context, _ *mcp.CallToolRequest, in ListScheduledTasksInput) (*mcp.CallToolResult, any, error) {
	q := url.Values{}
	if in.Limit != 0 {
		q.Set("limit", strconv.Itoa(in.Limit))
	}
	if in.Cursor != "" {
		q.Set("cursor", in.Cursor)
	}
	path := "/automations/v1"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return s.call(ctx, http.MethodGet, path, nil)
}

func (s *Server) getScheduledTask(ctx context.Context, _ *mcp.CallToolRequest, in ScheduledTaskRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodGet, scheduledTaskPath(in.AutomationID), nil)
}

func (s *Server) createScheduledTask(ctx context.Context, _ *mcp.CallToolRequest, in ScheduledTaskSettings) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, "/automations/v1", in)
}

func (s *Server) updateScheduledTask(ctx context.Context, _ *mcp.CallToolRequest, in UpdateScheduledTaskInput) (*mcp.CallToolResult, any, error) {
	if in.ClearAssistantID && in.AssistantID != nil {
		return nil, nil, fmt.Errorf("pass either assistantId or clearAssistantId, not both")
	}
	if in.ClearTaggedAssistantID && in.TaggedAssistantID != nil {
		return nil, nil, fmt.Errorf("pass either taggedAssistantId or clearTaggedAssistantId, not both")
	}
	// A map lets the clear flags send an explicit null, which the struct's
	// omitempty pointers cannot express.
	b, err := json.Marshal(in.ScheduledTaskSettings)
	if err != nil {
		return nil, nil, err
	}
	body := map[string]any{}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, nil, err
	}
	if in.ClearAssistantID {
		body["assistantId"] = nil
	}
	if in.ClearTaggedAssistantID {
		body["taggedAssistantId"] = nil
	}
	if len(body) == 0 {
		return nil, nil, fmt.Errorf("nothing to update: pass at least one field")
	}
	return s.call(ctx, http.MethodPatch, scheduledTaskPath(in.AutomationID), body)
}

func (s *Server) deleteScheduledTask(ctx context.Context, _ *mcp.CallToolRequest, in ScheduledTaskRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodDelete, scheduledTaskPath(in.AutomationID), nil)
}

func (s *Server) pauseScheduledTask(ctx context.Context, _ *mcp.CallToolRequest, in ScheduledTaskRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, scheduledTaskPath(in.AutomationID)+"/pause", nil)
}

func (s *Server) resumeScheduledTask(ctx context.Context, _ *mcp.CallToolRequest, in ScheduledTaskRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, scheduledTaskPath(in.AutomationID)+"/resume", nil)
}

func (s *Server) runScheduledTask(ctx context.Context, _ *mcp.CallToolRequest, in ScheduledTaskRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, scheduledTaskPath(in.AutomationID)+"/run", nil)
}

func scheduledTaskPath(id string) string {
	return "/automations/v1/" + url.PathEscape(id)
}

// scheduledTaskStatusHint covers the Scheduled Tasks API, which only accepts
// workspace keys of a service account with the AUTOMATION_API scope.
func scheduledTaskStatusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid request or task id, a schedule missing the clock fields its frequency needs, or AUTOMATION_LIMIT_REACHED (10 tasks per owner)"
	case http.StatusUnauthorized:
		return "invalid or missing API key"
	case http.StatusForbidden:
		return "API key lacks the AUTOMATION_API scope or is a personal key, Scheduled Tasks are disabled or not available to the key's service account, the trial expired, or a referenced agent, model or attachment is not accessible (AUTOMATION_TAG_ACCESS_DENIED)"
	case http.StatusNotFound:
		return "scheduled task not found, deleted, or owned by another API key"
	case http.StatusTooManyRequests:
		return "rate limit exceeded, retry later"
	}
	return ""
}
