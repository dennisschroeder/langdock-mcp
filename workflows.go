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
	workflowTriggerKinds = []string{"manual", "webhook", "scheduled", "form", "integration", "meeting_end"}
	workflowShareRoles   = []string{"user", "editor"}
	workflowStatuses     = []string{"ACTIVE", "INACTIVE"}
	workflowBumpTypes    = []string{"major", "minor", "patch"}
	workflowRunModes     = []string{"test", "production"}
	workflowRunStatuses  = []string{"PENDING", "IN_PROGRESS", "AWAITING_INPUT", "COMPLETED", "FAILED", "CANCELLED"}
	workflowLimitNames   = []string{"monthlyCostUsd", "perRunCostUsd", "maxExecutionsPerHour"}
)

type WorkflowRef struct {
	WorkflowID string `json:"workflowId" jsonschema:"UUID of the workflow"`
}

type ListWorkflowsInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"page size, 1-250, default 50"`
	Cursor string `json:"cursor,omitempty" jsonschema:"nextCursor from the previous page"`
}

type WorkflowShareWith struct {
	UserIDs  []string `json:"userIds,omitempty" jsonschema:"user UUIDs, max 100"`
	GroupIDs []string `json:"groupIds,omitempty" jsonschema:"group UUIDs, max 100"`
	Role     string   `json:"role,omitempty" jsonschema:"user (viewer) or editor, default editor"`
}

// WorkflowLimits are the execution caps. Pointers keep "omitted" distinct
// from a value; removing a cap needs an explicit null, which removeLimits sends.
type WorkflowLimits struct {
	MonthlyCostUsd       *float64 `json:"monthlyCostUsd,omitempty" jsonschema:"monthly cost cap in USD, 1-10000"`
	PerRunCostUsd        *float64 `json:"perRunCostUsd,omitempty" jsonschema:"cost cap per run in USD, 1-100"`
	MaxExecutionsPerHour *int     `json:"maxExecutionsPerHour,omitempty" jsonschema:"executions per hour, 1-5000"`
}

// WorkflowGraph is the draft graph in the workflow builder's node and edge
// schema, as returned by get_workflow.
type WorkflowGraph struct {
	Nodes *[]map[string]any `json:"nodes,omitempty" jsonschema:"draft nodes in the builder's schema, as returned by get_workflow; must be sent together with edges"`
	Edges *[]map[string]any `json:"edges,omitempty" jsonschema:"draft edges in the builder's schema, as returned by get_workflow; must be sent together with nodes"`
}

type CreateWorkflowInput struct {
	Name               string `json:"name" jsonschema:"1-60 characters"`
	Description        string `json:"description,omitempty" jsonschema:"max 500 characters"`
	Timezone           string `json:"timezone,omitempty" jsonschema:"IANA timezone such as Europe/Berlin"`
	InitialTriggerKind string `json:"initialTriggerKind,omitempty" jsonschema:"starter trigger; not together with nodes and edges. meeting_end needs Meetings enabled for the key user"`
	WorkflowGraph
	ShareWith    *WorkflowShareWith `json:"shareWith,omitempty" jsonschema:"users and groups to share with; needs the shareWorkflows permission"`
	Limits       *WorkflowLimits    `json:"limits,omitempty" jsonschema:"execution caps; use removeLimits to remove one; omitted caps use the builder defaults ($25 monthly or the workspace default, $2 per run, 100 executions per hour)"`
	RemoveLimits []string           `json:"removeLimits,omitempty" jsonschema:"caps to create without a limit (sent as null)"`
}

type UpdateWorkflowInput struct {
	WorkflowID  string  `json:"workflowId" jsonschema:"UUID of the workflow"`
	Name        *string `json:"name,omitempty" jsonschema:"1-60 characters"`
	Description *string `json:"description,omitempty" jsonschema:"max 500 characters"`
	Status      *string `json:"status,omitempty" jsonschema:"ACTIVE needs a published version; INACTIVE pauses a live workflow"`
	Timezone    *string `json:"timezone,omitempty" jsonschema:"IANA timezone; ignored when status is INACTIVE"`
	WorkflowGraph
	Patch        map[string]any  `json:"patch,omitempty" jsonschema:"incremental graph operations instead of a full nodes and edges replacement"`
	Limits       *WorkflowLimits `json:"limits,omitempty" jsonschema:"execution caps; omitted caps stay unchanged; use removeLimits to remove one"`
	RemoveLimits []string        `json:"removeLimits,omitempty" jsonschema:"caps to remove (sent as null)"`
}

type PublishWorkflowInput struct {
	WorkflowID  string `json:"workflowId" jsonschema:"UUID of the workflow"`
	BumpType    string `json:"bumpType" jsonschema:"version bump"`
	Description string `json:"description,omitempty" jsonschema:"version description, max 255 characters"`
	Timezone    string `json:"timezone,omitempty" jsonschema:"IANA timezone; stored only when valid"`
}

type ListWorkflowRunsInput struct {
	WorkflowID string `json:"workflowId" jsonschema:"UUID of the workflow"`
	Limit      int    `json:"limit,omitempty" jsonschema:"page size, 1-100, default 50"`
	Cursor     string `json:"cursor,omitempty" jsonschema:"nextCursor from the previous page"`
	RunID      string `json:"runId,omitempty" jsonschema:"only this run"`
	RunMode    string `json:"runMode,omitempty" jsonschema:"test runs use version 0, production runs a published version"`
	Status     string `json:"status,omitempty" jsonschema:"run status"`
	From       string `json:"from,omitempty" jsonschema:"start of the run creation range, ISO 8601 timestamp or YYYY-MM-DD (00:00 UTC); must be sent with to"`
	To         string `json:"to,omitempty" jsonschema:"end of the run creation range, ISO 8601 timestamp or YYYY-MM-DD (23:59:59.999 UTC); must be sent with from"`
	Version    string `json:"version,omitempty" jsonschema:"workflow version of the run; 0 is the draft used for test runs"`
}

type ExportWorkflowRunsInput struct {
	WorkflowID string `json:"workflowId" jsonschema:"UUID of the workflow"`
	From       string `json:"from" jsonschema:"start of the run creation range, ISO 8601 date (00:00 UTC) or timestamp with Z or an offset"`
	To         string `json:"to" jsonschema:"end of the run creation range, ISO 8601 date (23:59:59.999 UTC) or timestamp with Z or an offset"`
}

func (s *Server) registerWorkflows() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)}
	additive := &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)}
	destructive := &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)}

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_workflows",
		Description: "List the non-template workflows the API key can see, with status, owner, the key's accessRole and the active version, but without the graph. Pass nextCursor as cursor for the next page. Workspace API keys see every workflow in the workspace.",
		Annotations: readOnly,
		InputSchema: schemaFor[ListWorkflowsInput](func(sc *jsonschema.Schema) {
			at(sc, "limit").Minimum = ptr(1.0)
			at(sc, "limit").Maximum = ptr(250.0)
		}),
	}, s.listWorkflows)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "get_workflow",
		Description: "Get a workflow: nodes and edges are the draft graph (version 0), activeVersion the published graph if one exists, plus version history and execution limits. Secret-like fields are redacted; resending them redacted through update_workflow keeps the stored secrets.",
		Annotations: readOnly,
		InputSchema: schemaFor[WorkflowRef](workflowIDs),
	}, s.getWorkflow)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "create_workflow",
		Description: "Create an inactive, unpublished draft workflow owned by the API key's user. Start it either from initialTriggerKind or from a full graph (nodes and edges), not both. It stays private to the key until shared via shareWith or later in the UI, and schedules and webhooks only start after publish_workflow.",
		Annotations: additive,
		InputSchema: schemaFor[CreateWorkflowInput](func(sc *jsonschema.Schema) {
			workflowSchema(sc)
			enum(sc, workflowTriggerKinds, "initialTriggerKind")
			at(sc, "shareWith", "userIds").MaxItems = ptr(100)
			at(sc, "shareWith", "groupIds").MaxItems = ptr(100)
			enum(sc, workflowShareRoles, "shareWith", "role")
		}),
	}, s.createWorkflow)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_workflow",
		Description: "Update a workflow's metadata or its draft graph, never both in one call. Metadata (name, description, status, timezone, limits) changes only the fields you pass; omitted limits stay unchanged and removeLimits removes caps. A graph update either replaces the whole draft with nodes and edges (both required, so pass the full graph from get_workflow) or applies incremental patch operations. Graph changes only touch the draft; the published version stays live until publish_workflow. Set status INACTIVE to pause a live workflow.",
		Annotations: destructive,
		InputSchema: schemaFor[UpdateWorkflowInput](func(sc *jsonschema.Schema) {
			workflowSchema(sc)
			enum(sc, workflowStatuses, "status")
			for _, name := range []string{"name", "description", "status", "timezone"} {
				notNull(at(sc, name), "string")
			}
		}),
	}, s.updateWorkflow)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "publish_workflow",
		Description: "Publish the current draft as a new production version. The version becomes active, scheduled and webhook triggers start, and the workflow is marked ACTIVE. Rejected when a trigger is disconnected, or when a webhook is unauthenticated and the workspace requires webhook auth.",
		Annotations: destructive,
		InputSchema: schemaFor[PublishWorkflowInput](func(sc *jsonschema.Schema) {
			workflowIDs(sc)
			enum(sc, workflowBumpTypes, "bumpType")
			maxLen(sc, 255, "description")
		}),
	}, s.publishWorkflow)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "delete_workflow",
		Description: "Permanently delete a workflow, including its draft, all published versions and its schedules and webhooks. This cannot be undone and asks for no confirmation. Needs a workspace API key with the WORKFLOW_DELETE_API scope and editor access. To stop a workflow without deleting it, use update_workflow with status INACTIVE.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(true)},
		InputSchema: schemaFor[WorkflowRef](workflowIDs),
	}, s.deleteWorkflow)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_workflow_runs",
		Description: "List a workflow's runs, newest first, with node executions (input, output, errors, logs). Pass nextCursor as cursor for the next page. Filters: runId, runMode, status, version and a from/to creation range. Needs editor access.",
		Annotations: readOnly,
		InputSchema: schemaFor[ListWorkflowRunsInput](func(sc *jsonschema.Schema) {
			workflowIDs(sc)
			at(sc, "limit").Minimum = ptr(1.0)
			at(sc, "limit").Maximum = ptr(100.0)
			enum(sc, workflowRunModes, "runMode")
			enum(sc, workflowRunStatuses, "status")
		}),
	}, s.listWorkflowRuns)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "export_workflow_runs",
		Description: "Export a workflow's runs in a creation date range as flat rows, one per node execution, oldest first. Not paginated and capped at 10,000 runs and 8,000,000 payload bytes; narrow the range if it fails. Input and output are kept for 30 days. Needs owner or editor access; a User share is not enough. Use list_workflow_runs for filters and paging.",
		Annotations: readOnly,
		InputSchema: schemaFor[ExportWorkflowRunsInput](func(sc *jsonschema.Schema) {
			workflowIDs(sc)
			at(sc, "from").MinLength = ptr(1)
			at(sc, "to").MinLength = ptr(1)
		}),
	}, s.exportWorkflowRuns)
}

// workflowIDs requires a non-empty id, because the export path would
// otherwise become /workflows//runs.
func workflowIDs(sc *jsonschema.Schema) {
	at(sc, "workflowId").MinLength = ptr(1)
}

func workflowSchema(sc *jsonschema.Schema) {
	if _, ok := sc.Properties["workflowId"]; ok {
		workflowIDs(sc)
	}
	at(sc, "name").MinLength = ptr(1)
	maxLen(sc, 60, "name")
	maxLen(sc, 500, "description")
	limits := at(sc, "limits")
	for name, hi := range map[string]float64{"monthlyCostUsd": 10000, "perRunCostUsd": 100, "maxExecutionsPerHour": 5000} {
		c := at(limits, name)
		c.Minimum = ptr(1.0)
		c.Maximum = ptr(hi)
		notNull(c, "number")
	}
	notNull(at(limits, "maxExecutionsPerHour"), "integer")
	enum(sc, workflowLimitNames, "removeLimits", "[]")
}

// notNull drops the null alternative jsonschema-go infers for pointers,
// because a null would decode to nil and be dropped silently instead of
// clearing the field as the docs describe; removeLimits sends the nulls.
func notNull(sc *jsonschema.Schema, typ string) {
	sc.Types = nil
	sc.Type = typ
}

func (s *Server) listWorkflows(ctx context.Context, _ *mcp.CallToolRequest, in ListWorkflowsInput) (*mcp.CallToolResult, any, error) {
	q := url.Values{}
	if in.Limit != 0 {
		q.Set("limit", strconv.Itoa(in.Limit))
	}
	setQuery(q, "cursor", in.Cursor)
	return s.call(ctx, http.MethodGet, withQuery("/workflows/v1/list", q), nil)
}

func (s *Server) getWorkflow(ctx context.Context, _ *mcp.CallToolRequest, in WorkflowRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodGet, "/workflows/v1/get?workflowId="+url.QueryEscape(in.WorkflowID), nil)
}

func (s *Server) createWorkflow(ctx context.Context, _ *mcp.CallToolRequest, in CreateWorkflowInput) (*mcp.CallToolResult, any, error) {
	if err := checkGraph(in.WorkflowGraph); err != nil {
		return nil, nil, err
	}
	if in.InitialTriggerKind != "" && in.Nodes != nil {
		return nil, nil, fmt.Errorf("pass either initialTriggerKind or nodes and edges, not both")
	}
	body, err := workflowBody(in, in.Limits, in.RemoveLimits)
	if err != nil {
		return nil, nil, err
	}
	return s.call(ctx, http.MethodPost, "/workflows/v1/create", body)
}

// updateWorkflow is a plain pass-through PATCH: the API leaves omitted
// metadata unchanged, so reading first would add nothing but a race.
func (s *Server) updateWorkflow(ctx context.Context, _ *mcp.CallToolRequest, in UpdateWorkflowInput) (*mcp.CallToolResult, any, error) {
	if err := checkGraph(in.WorkflowGraph); err != nil {
		return nil, nil, err
	}
	fullGraph := in.Nodes != nil
	patch := len(in.Patch) > 0
	metadata := in.Name != nil || in.Description != nil || in.Status != nil || in.Timezone != nil || in.Limits != nil || len(in.RemoveLimits) > 0
	switch {
	case fullGraph && patch:
		return nil, nil, fmt.Errorf("pass either nodes and edges or patch, not both")
	case (fullGraph || patch) && metadata:
		return nil, nil, fmt.Errorf("the API rejects graph and metadata changes in one request; update them in separate calls")
	case !fullGraph && !patch && !metadata:
		return nil, nil, fmt.Errorf("nothing to update: pass metadata, nodes and edges, or patch")
	}
	body, err := workflowBody(in, in.Limits, in.RemoveLimits)
	if err != nil {
		return nil, nil, err
	}
	return s.call(ctx, http.MethodPatch, "/workflows/v1/update", body)
}

func checkGraph(g WorkflowGraph) error {
	if (g.Nodes == nil) != (g.Edges == nil) {
		return fmt.Errorf("nodes and edges must be sent together")
	}
	return nil
}

// workflowBody flattens the input into a map so removed limits can be sent
// as explicit nulls, which the struct's omitempty pointers cannot express.
func workflowBody(in any, limits *WorkflowLimits, remove []string) (map[string]any, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, err
	}
	delete(body, "removeLimits")
	if len(remove) == 0 {
		return body, nil
	}
	merged := map[string]any{}
	if limits != nil {
		lb, _ := json.Marshal(limits)
		json.Unmarshal(lb, &merged)
	}
	for _, name := range remove {
		if v, set := merged[name]; set && v != nil {
			return nil, fmt.Errorf("limit %s is both set and removed", name)
		}
		merged[name] = nil
	}
	body["limits"] = merged
	return body, nil
}

func (s *Server) publishWorkflow(ctx context.Context, _ *mcp.CallToolRequest, in PublishWorkflowInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, "/workflows/v1/publish", in)
}

func (s *Server) deleteWorkflow(ctx context.Context, _ *mcp.CallToolRequest, in WorkflowRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodDelete, "/workflows/v1/delete?workflowId="+url.QueryEscape(in.WorkflowID), nil)
}

func (s *Server) listWorkflowRuns(ctx context.Context, _ *mcp.CallToolRequest, in ListWorkflowRunsInput) (*mcp.CallToolResult, any, error) {
	if (in.From == "") != (in.To == "") {
		return nil, nil, fmt.Errorf("from and to must be sent together")
	}
	q := url.Values{}
	q.Set("workflowId", in.WorkflowID)
	if in.Limit != 0 {
		q.Set("limit", strconv.Itoa(in.Limit))
	}
	setQuery(q, "cursor", in.Cursor)
	setQuery(q, "runId", in.RunID)
	setQuery(q, "runMode", in.RunMode)
	setQuery(q, "status", in.Status)
	setQuery(q, "from", in.From)
	setQuery(q, "to", in.To)
	setQuery(q, "version", in.Version)
	return s.call(ctx, http.MethodGet, withQuery("/workflows/v1/runs", q), nil)
}

func (s *Server) exportWorkflowRuns(ctx context.Context, _ *mcp.CallToolRequest, in ExportWorkflowRunsInput) (*mcp.CallToolResult, any, error) {
	q := url.Values{}
	q.Set("from", in.From)
	q.Set("to", in.To)
	return s.call(ctx, http.MethodGet, withQuery("/workflows/"+url.PathEscape(in.WorkflowID)+"/runs", q), nil)
}

func setQuery(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// workflowStatusHint covers the Workflow API, whose three scopes split read,
// write and delete access, and whose write endpoints report a missing
// workflow as 403 rather than 404.
func workflowStatusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid parameters, graph and metadata mixed in one update, nodes without edges, activating a workflow without a published version, run filters with to earlier than from or a runMode that disagrees with version, or a draft that cannot be published (disconnected trigger, unauthenticated webhook)"
	case http.StatusUnauthorized:
		return "invalid or missing API key"
	case http.StatusForbidden:
		return "API key lacks the needed scope (WORKFLOW_API to read, WORKFLOW_WRITE_API to create, update and publish, WORKFLOW_DELETE_API to delete), the createWorkflows or shareWorkflows permission, or editor access; update, publish and runs also return 403 for a missing workflow or a template"
	case http.StatusNotFound:
		return "workflow not found or not accessible to the API key, or a shareWith target is unknown"
	case http.StatusTooManyRequests:
		return "rate limit exceeded, retry later"
	}
	return ""
}

// workflowExportStatusHint covers the Workflow Run Export API, where 400 also
// signals the run and payload caps.
func workflowExportStatusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid workflow id or date, to earlier than from, or more than 10,000 runs or 8,000,000 payload bytes in the range; narrow the date range"
	case http.StatusUnauthorized:
		return "invalid or missing API key"
	case http.StatusForbidden:
		return "API key lacks the WORKFLOW_API scope, or its user is not the workflow's owner, editor or a workspace admin; a User share is not enough"
	case http.StatusTooManyRequests:
		return "rate limit of 500 requests/minute exceeded, retry later"
	}
	return ""
}
