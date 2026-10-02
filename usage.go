package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// usageGroupBy lists the group_by values each export type accepts; the API
// rejects all others with 400. deployment only works in BYOK workspaces.
var usageGroupBy = map[string][]string{
	"users":     {"model"},
	"agents":    {"model"},
	"api-keys":  {"model"},
	"projects":  nil,
	"models":    {"source", "deployment"},
	"workflows": nil,
}

var (
	usageDataTypes = []string{"users", "agents", "api-keys", "projects", "models", "workflows"}
	usageFormats   = []string{"json", "csv"}
)

type UsageTime struct {
	Date     string `json:"date" jsonschema:"local time in ISO 8601, e.g. 2024-01-01T00:00:00.000; a trailing Z is stripped by the API, so the time is always read in timezone"`
	Timezone string `json:"timezone" jsonschema:"IANA time zone the date is read in, e.g. UTC or Europe/Berlin"`
}

type ExportUsageInput struct {
	DataType string    `json:"dataType" jsonschema:"what to export"`
	From     UsageTime `json:"from" jsonschema:"start of the period"`
	To       UsageTime `json:"to" jsonschema:"end of the period"`
	GroupBy  string    `json:"group_by,omitempty" jsonschema:"optional aggregation: model for users, agents and api-keys; source or deployment (BYOK only) for models; not supported for projects and workflows"`
	Format   string    `json:"format,omitempty" jsonschema:"json (default) returns the rows inline; csv returns a signed download URL instead, for exports too large to return inline"`
}

func (s *Server) registerUsage() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "export_usage",
		Description: "Export workspace usage for users, agents, API keys, projects, models or workflows over a period. Needs an API key with the USAGE_EXPORT_API scope, which exposes usage data of the whole workspace. User-identifying columns depend on the workspace privacy settings. One request may scan at most 1,000,000 usage rows; split longer periods and combine the results.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
		InputSchema: schemaFor[ExportUsageInput](func(sc *jsonschema.Schema) {
			enum(sc, usageDataTypes, "dataType")
			enum(sc, []string{"model", "source", "deployment"}, "group_by")
			enum(sc, usageFormats, "format")
		}),
	}, s.exportUsage)
}

type usageExportBody struct {
	From    UsageTime `json:"from"`
	To      UsageTime `json:"to"`
	GroupBy string    `json:"group_by,omitempty"`
}

func (s *Server) exportUsage(ctx context.Context, _ *mcp.CallToolRequest, in ExportUsageInput) (*mcp.CallToolResult, any, error) {
	allowed, ok := usageGroupBy[in.DataType]
	if !ok {
		return nil, nil, fmt.Errorf("unknown dataType %q", in.DataType)
	}
	if in.GroupBy != "" && !slices.Contains(allowed, in.GroupBy) {
		if len(allowed) == 0 {
			return nil, nil, fmt.Errorf("group_by is not supported for %s exports", in.DataType)
		}
		return nil, nil, fmt.Errorf("group_by for %s exports must be one of: %s", in.DataType, strings.Join(allowed, ", "))
	}
	format := in.Format
	if format == "" {
		format = "json"
	}
	// Both format routes answer with a JSON envelope (rows, or a download URL
	// for csv), so the default route's format is never needed.
	res, out, err := s.call(ctx, http.MethodPost, "/export/"+in.DataType+"/"+format, usageExportBody{in.From, in.To, in.GroupBy})
	if errors.Is(err, errResponseTooLarge) && format == "json" {
		err = fmt.Errorf("%w; use format csv to get a download URL, or a shorter period", err)
	}
	return res, out, err
}

// usageStatusHint covers the Usage Export API, which needs its own
// USAGE_EXPORT_API scope that only workspace admins can grant.
func usageStatusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid date range, group_by not supported for this export, or more than 1,000,000 usage rows (USAGE_EXPORT_TOO_LARGE); use a shorter period and combine the results"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "invalid or missing API key, or the key lacks the USAGE_EXPORT_API scope"
	case http.StatusNotFound:
		return "no usage data in the selected period"
	case http.StatusTooManyRequests:
		return "rate limit of 500 requests/minute exceeded, retry later"
	}
	return ""
}
