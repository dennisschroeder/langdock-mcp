package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// uuidPattern guards path ids, because a non-UUID id such as "folders" or ""
// silently addresses a different route under /prompts/v1. Body ids get it too,
// so a malformed one fails before the request instead of with a 400.
const uuidPattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`

type ListPromptsInput struct {
	Limit               int    `json:"limit,omitempty" jsonschema:"page size, 1-250, default 50"`
	Cursor              string `json:"cursor,omitempty" jsonschema:"nextCursor from the previous page"`
	Query               string `json:"query,omitempty" jsonschema:"search in prompt title and content"`
	PromptFolderID      string `json:"promptFolderId,omitempty" jsonschema:"only prompts in this folder (UUID)"`
	SharedWithWorkspace *bool  `json:"sharedWithWorkspace,omitempty" jsonschema:"only prompts that are (true) or are not (false) shared with the workspace"`
}

type PromptRef struct {
	PromptID string `json:"promptId" jsonschema:"UUID of the prompt"`
}

type CreatePromptInput struct {
	Title               string `json:"title" jsonschema:"2-100 characters"`
	Prompt              string `json:"prompt" jsonschema:"prompt content, 2-120000 characters"`
	PromptFolderID      string `json:"promptFolderId,omitempty" jsonschema:"UUID of the folder to put the prompt in; not allowed together with sharedWithWorkspace true"`
	SharedWithWorkspace *bool  `json:"sharedWithWorkspace,omitempty" jsonschema:"share with the workspace, needs the sharePrompts permission; not allowed together with promptFolderId"`
}

type UpdatePromptInput struct {
	PromptID            string  `json:"promptId" jsonschema:"UUID of the prompt"`
	Title               *string `json:"title,omitempty" jsonschema:"2-100 characters"`
	Prompt              *string `json:"prompt,omitempty" jsonschema:"prompt content, 2-120000 characters"`
	PromptFolderID      *string `json:"promptFolderId,omitempty" jsonschema:"UUID of the folder to move the prompt to; not allowed while the prompt is shared with the workspace"`
	ClearPromptFolderID bool    `json:"clearPromptFolderId,omitempty" jsonschema:"set to take the prompt out of its folder"`
	SharedWithWorkspace *bool   `json:"sharedWithWorkspace,omitempty" jsonschema:"share with the workspace, needs the sharePrompts permission; true is not allowed while the prompt is in a folder"`
}

type ListPromptFoldersInput struct {
	Limit               int    `json:"limit,omitempty" jsonschema:"page size, 1-250, default 50"`
	Cursor              string `json:"cursor,omitempty" jsonschema:"nextCursor from the previous page"`
	Query               string `json:"query,omitempty" jsonschema:"search in folder names"`
	SharedWithWorkspace *bool  `json:"sharedWithWorkspace,omitempty" jsonschema:"only folders that are (true) or are not (false) shared with the workspace"`
}

type PromptFolderRef struct {
	FolderID string `json:"folderId" jsonschema:"UUID of the prompt folder"`
}

type CreatePromptFolderInput struct {
	Name                string `json:"name" jsonschema:"2-50 characters"`
	SharedWithWorkspace *bool  `json:"sharedWithWorkspace,omitempty" jsonschema:"share with the workspace; not allowed together with sharedWithGroupId"`
	SharedWithGroupID   string `json:"sharedWithGroupId,omitempty" jsonschema:"UUID of a group to share with; not allowed together with sharedWithWorkspace true"`
}

type UpdatePromptFolderInput struct {
	FolderID               string  `json:"folderId" jsonschema:"UUID of the prompt folder"`
	Name                   *string `json:"name,omitempty" jsonschema:"2-50 characters"`
	SharedWithWorkspace    *bool   `json:"sharedWithWorkspace,omitempty" jsonschema:"share with the workspace; true is not allowed while the folder is shared with a group"`
	SharedWithGroupID      *string `json:"sharedWithGroupId,omitempty" jsonschema:"UUID of a group to share with; not allowed while the folder is shared with the workspace"`
	ClearSharedWithGroupID bool    `json:"clearSharedWithGroupId,omitempty" jsonschema:"set to stop sharing the folder with its group"`
}

func (s *Server) registerPrompts() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)}
	additive := &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)}
	destructive := &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)}

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_prompts",
		Description: "List the Prompt Library prompts the API key's owner can access (own, workspace-shared, or in a shared folder). Paginated: pass nextCursor as cursor for the next page.",
		Annotations: readOnly,
		InputSchema: schemaFor[ListPromptsInput](listSchema),
	}, s.listPrompts)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "create_prompt",
		Description: "Create a Prompt Library prompt owned by the API key's owner. A prompt is either in a folder or shared with the workspace, never both.",
		Annotations: additive,
		InputSchema: schemaFor[CreatePromptInput](promptSchema),
	}, s.createPrompt)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "get_prompt",
		Description: "Get a Prompt Library prompt by id.",
		Annotations: readOnly,
		InputSchema: schemaFor[PromptRef](uuidIDs),
	}, s.getPrompt)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_prompt",
		Description: "Update a Prompt Library prompt. Only the fields you pass change. clearPromptFolderId takes it out of its folder. A prompt cannot be in a folder and shared with the workspace at the same time, so unset one before setting the other.",
		Annotations: destructive,
		InputSchema: schemaFor[UpdatePromptInput](promptSchema),
	}, s.updatePrompt)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "delete_prompt",
		Description: "Permanently delete a Prompt Library prompt. Cannot be undone.",
		Annotations: destructive,
		InputSchema: schemaFor[PromptRef](uuidIDs),
	}, s.deletePrompt)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_prompt_folders",
		Description: "List the Prompt Library folders the API key's owner can access (own, workspace-shared or group-shared). Paginated: pass nextCursor as cursor for the next page.",
		Annotations: readOnly,
		InputSchema: schemaFor[ListPromptFoldersInput](listSchema),
	}, s.listPromptFolders)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "create_prompt_folder",
		Description: "Create a Prompt Library folder owned by the API key's owner. A folder is shared with the workspace or with one group, never both; without either it stays private to its creator.",
		Annotations: additive,
		InputSchema: schemaFor[CreatePromptFolderInput](promptFolderSchema),
	}, s.createPromptFolder)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "get_prompt_folder",
		Description: "Get a Prompt Library folder by id. Use list_prompts with promptFolderId to see its prompts.",
		Annotations: readOnly,
		InputSchema: schemaFor[PromptFolderRef](uuidIDs),
	}, s.getPromptFolder)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_prompt_folder",
		Description: "Update a Prompt Library folder. Only the fields you pass change. clearSharedWithGroupId stops sharing it with its group. A folder cannot be shared with the workspace and a group at the same time, so unset one before setting the other.",
		Annotations: destructive,
		InputSchema: schemaFor[UpdatePromptFolderInput](promptFolderSchema),
	}, s.updatePromptFolder)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "delete_prompt_folder",
		Description: "Permanently delete a Prompt Library folder and every prompt in it. Cannot be undone.",
		Annotations: destructive,
		InputSchema: schemaFor[PromptFolderRef](uuidIDs),
	}, s.deletePromptFolder)
}

func uuidIDs(sc *jsonschema.Schema) {
	for _, p := range []string{"promptId", "folderId", "promptFolderId", "sharedWithGroupId"} {
		if prop, ok := sc.Properties[p]; ok {
			prop.Pattern = uuidPattern
		}
	}
}

// nonNull rejects an explicit null on pointer fields, which the inferred
// schema allows. The handler would decode it as omitted and drop it, although
// the API documents null as the way to unset promptFolderId and
// sharedWithGroupId; the clear* flags send that null instead.
func nonNull(sc *jsonschema.Schema, props ...string) {
	for _, p := range props {
		prop := at(sc, p)
		var types []string
		for _, t := range prop.Types {
			if t != "null" {
				types = append(types, t)
			}
		}
		if len(types) == 1 {
			prop.Type, prop.Types = types[0], nil
		} else if len(prop.Types) > 0 {
			prop.Types = types
		}
	}
}

func listSchema(sc *jsonschema.Schema) {
	nonNull(sc, "sharedWithWorkspace")
	at(sc, "limit").Minimum = ptr(1.0)
	at(sc, "limit").Maximum = ptr(250.0)
}

func promptSchema(sc *jsonschema.Schema) {
	uuidIDs(sc)
	nonNull(sc, "title", "prompt", "promptFolderId", "sharedWithWorkspace")
	at(sc, "title").MinLength = ptr(2)
	maxLen(sc, 100, "title")
	at(sc, "prompt").MinLength = ptr(2)
	maxLen(sc, 120000, "prompt")
}

func promptFolderSchema(sc *jsonschema.Schema) {
	uuidIDs(sc)
	nonNull(sc, "name", "sharedWithWorkspace", "sharedWithGroupId")
	at(sc, "name").MinLength = ptr(2)
	maxLen(sc, 50, "name")
}

func (s *Server) listPrompts(ctx context.Context, _ *mcp.CallToolRequest, in ListPromptsInput) (*mcp.CallToolResult, any, error) {
	q := listQuery(in.Limit, in.Cursor, in.Query, in.SharedWithWorkspace)
	if in.PromptFolderID != "" {
		q.Set("promptFolderId", in.PromptFolderID)
	}
	return s.call(ctx, http.MethodGet, withQuery("/prompts/v1", q), nil)
}

func (s *Server) createPrompt(ctx context.Context, _ *mcp.CallToolRequest, in CreatePromptInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, "/prompts/v1", in)
}

func (s *Server) getPrompt(ctx context.Context, _ *mcp.CallToolRequest, in PromptRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodGet, promptPath(in.PromptID), nil)
}

func (s *Server) updatePrompt(ctx context.Context, _ *mcp.CallToolRequest, in UpdatePromptInput) (*mcp.CallToolResult, any, error) {
	if in.ClearPromptFolderID && in.PromptFolderID != nil {
		return nil, nil, fmt.Errorf("pass either promptFolderId or clearPromptFolderId, not both")
	}
	// A map, because clearing the folder needs an explicit null.
	body := map[string]any{}
	setIf(body, "title", in.Title)
	setIf(body, "prompt", in.Prompt)
	setIf(body, "promptFolderId", in.PromptFolderID)
	setIf(body, "sharedWithWorkspace", in.SharedWithWorkspace)
	if in.ClearPromptFolderID {
		body["promptFolderId"] = nil
	}
	if len(body) == 0 {
		return nil, nil, fmt.Errorf("nothing to update: pass at least one field")
	}
	return s.call(ctx, http.MethodPatch, promptPath(in.PromptID), body)
}

func (s *Server) deletePrompt(ctx context.Context, _ *mcp.CallToolRequest, in PromptRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodDelete, promptPath(in.PromptID), nil)
}

func (s *Server) listPromptFolders(ctx context.Context, _ *mcp.CallToolRequest, in ListPromptFoldersInput) (*mcp.CallToolResult, any, error) {
	q := listQuery(in.Limit, in.Cursor, in.Query, in.SharedWithWorkspace)
	return s.call(ctx, http.MethodGet, withQuery("/prompts/v1/folders", q), nil)
}

func (s *Server) createPromptFolder(ctx context.Context, _ *mcp.CallToolRequest, in CreatePromptFolderInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, "/prompts/v1/folders", in)
}

func (s *Server) getPromptFolder(ctx context.Context, _ *mcp.CallToolRequest, in PromptFolderRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodGet, promptFolderPath(in.FolderID), nil)
}

func (s *Server) updatePromptFolder(ctx context.Context, _ *mcp.CallToolRequest, in UpdatePromptFolderInput) (*mcp.CallToolResult, any, error) {
	if in.ClearSharedWithGroupID && in.SharedWithGroupID != nil {
		return nil, nil, fmt.Errorf("pass either sharedWithGroupId or clearSharedWithGroupId, not both")
	}
	body := map[string]any{}
	setIf(body, "name", in.Name)
	setIf(body, "sharedWithWorkspace", in.SharedWithWorkspace)
	setIf(body, "sharedWithGroupId", in.SharedWithGroupID)
	if in.ClearSharedWithGroupID {
		body["sharedWithGroupId"] = nil
	}
	if len(body) == 0 {
		return nil, nil, fmt.Errorf("nothing to update: pass at least one field")
	}
	return s.call(ctx, http.MethodPatch, promptFolderPath(in.FolderID), body)
}

func (s *Server) deletePromptFolder(ctx context.Context, _ *mcp.CallToolRequest, in PromptFolderRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodDelete, promptFolderPath(in.FolderID), nil)
}

func setIf[T any](body map[string]any, key string, v *T) {
	if v != nil {
		body[key] = *v
	}
}

func listQuery(limit int, cursor, query string, shared *bool) url.Values {
	q := url.Values{}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if query != "" {
		q.Set("query", query)
	}
	if shared != nil {
		q.Set("sharedWithWorkspace", strconv.FormatBool(*shared))
	}
	return q
}

func promptPath(id string) string {
	return "/prompts/v1/" + url.PathEscape(id)
}

func promptFolderPath(id string) string {
	return "/prompts/v1/folders/" + url.PathEscape(id)
}

// promptStatusHint covers the Prompt Library API, where 403 also means the
// key owner lacks the sharePrompts permission or write access.
func promptStatusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid parameters or ID format, no field to update, or workspace sharing combined with a folder (prompts) or a group (folders)"
	case http.StatusUnauthorized:
		return "invalid or missing API key"
	case http.StatusForbidden:
		return "API key lacks the PROMPT_API scope, or its owner lacks write access or the sharePrompts permission"
	case http.StatusNotFound:
		return "prompt, prompt folder or group not found, or not accessible to the API key's owner"
	case http.StatusTooManyRequests:
		return "rate limit exceeded, retry later"
	}
	return ""
}
