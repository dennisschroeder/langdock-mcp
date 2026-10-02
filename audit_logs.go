package main

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// auditLogEntityTypes are the documented examples, not an exhaustive list,
// so they are schema examples rather than an enum that would reject newer
// entity types.
var auditLogEntityTypes = []string{"User", "Workspace", "Group", "IntegrationConnection", "ModelConfig", "Workflow", "SkillAccess", "ActionAccess"}

// ListAuditLogsInput uses the API's snake_case query parameter names, unlike
// the other APIs, so they still match the docs one-to-one.
type ListAuditLogsInput struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"UUID of the workspace the API key belongs to"`
	From        string `json:"from,omitempty" jsonschema:"start of the date range, ISO 8601 date-time"`
	To          string `json:"to,omitempty" jsonschema:"end of the date range, ISO 8601 date-time"`
	EntityType  string `json:"entity_type,omitempty" jsonschema:"filter by entity type, e.g. User, Workspace, Group, IntegrationConnection, ModelConfig, Workflow, SkillAccess, ActionAccess"`
	ActorID     string `json:"actor_id,omitempty" jsonschema:"filter by the UUID of the actor who performed the action"`
	Limit       int    `json:"limit,omitempty" jsonschema:"page size, 1-50, default 50"`
	Cursor      string `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page"`
}

func (s *Server) registerAuditLogs() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_audit_logs",
		Description: "List the workspace's audit log entries: who (actor_id, actor_type USER / API_KEY / SYSTEM / SCIM, actor_name) did what (action in dot notation, e.g. user.updated) to which entity (entity_type, entity_id), when (created_at) and from where (ip_address, user_agent), with a before/after diff in changes and the entity or event metadata in snapshot. Pass next_cursor as cursor for the next page; it is null on the last page. Entries are retained for 90 days. Needs an API key with the AUDIT_LOG_API scope from the same workspace.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
		InputSchema: schemaFor[ListAuditLogsInput](func(sc *jsonschema.Schema) {
			at(sc, "workspace_id").MinLength = ptr(1)
			at(sc, "limit").Minimum = ptr(1.0)
			at(sc, "limit").Maximum = ptr(50.0)
			for _, p := range []string{"from", "to"} {
				at(sc, p).Format = "date-time"
			}
			for _, p := range []string{"workspace_id", "actor_id", "cursor"} {
				at(sc, p).Format = "uuid"
			}
			entity := at(sc, "entity_type")
			for _, v := range auditLogEntityTypes {
				entity.Examples = append(entity.Examples, v)
			}
		}),
	}, s.listAuditLogs)
}

func (s *Server) listAuditLogs(ctx context.Context, _ *mcp.CallToolRequest, in ListAuditLogsInput) (*mcp.CallToolResult, any, error) {
	q := url.Values{}
	for k, v := range map[string]string{"from": in.From, "to": in.To, "entity_type": in.EntityType, "actor_id": in.ActorID, "cursor": in.Cursor} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if in.Limit != 0 {
		q.Set("limit", strconv.Itoa(in.Limit))
	}
	path := "/audit-logs/" + url.PathEscape(in.WorkspaceID)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return s.call(ctx, http.MethodGet, path, nil)
}
