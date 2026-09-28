package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName    = "langdock-mcp"
	serverVersion = "0.1.0"
	maxIconBytes  = 20 << 20
)

var (
	actionFieldTypes  = []string{"TEXT", "MULTI_LINE_TEXT", "NUMBER", "BOOLEAN", "SELECT", "PASSWORD", "VECTOR", "OBJECT", "FILE", "ID"}
	triggerFieldTypes = []string{"TEXT", "MULTI_LINE_TEXT", "NUMBER", "BOOLEAN", "SELECT", "PASSWORD", "VECTOR"}
	authFieldTypes    = []string{"TEXT", "MULTI_LINE_TEXT", "PASSWORD", "NUMBER", "EMBEDDING_MODEL"}
	authTypes         = []string{"NONE", "API_KEY", "OAUTH", "OAUTH_DCR"}
)

// Tool inputs mirror the API's camelCase field names so what the model sees
// matches the Langdock docs one-to-one.

type Option struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// InputField is an action input field. It doubles as the decode target for
// fields returned by get_integration, which drops response-only properties
// (slug, order) that the write endpoints don't accept.
type InputField struct {
	Label            string   `json:"label" jsonschema:"field label, max 100 characters"`
	Type             string   `json:"type,omitempty" jsonschema:"field type, default TEXT"`
	Description      string   `json:"description,omitempty" jsonschema:"help text shown to the agent, max 500 characters"`
	Placeholder      string   `json:"placeholder,omitempty" jsonschema:"max 200 characters"`
	Required         bool     `json:"required,omitempty"`
	Options          []Option `json:"options,omitempty" jsonschema:"choices for SELECT fields"`
	AllowMultiSelect bool     `json:"allowMultiSelect,omitempty" jsonschema:"allow multiple selections for SELECT fields"`
	ContextActionID  string   `json:"contextActionId,omitempty" jsonschema:"UUID of an action that provides dynamic options"`
	JSONSchema       string   `json:"jsonSchema,omitempty" jsonschema:"stringified JSON Schema constraining the value, typically for OBJECT fields"`
}

type TriggerInputField struct {
	Label            string   `json:"label" jsonschema:"field label, max 100 characters"`
	Type             string   `json:"type,omitempty" jsonschema:"field type, default TEXT"`
	Description      string   `json:"description,omitempty" jsonschema:"max 500 characters"`
	Placeholder      string   `json:"placeholder,omitempty" jsonschema:"max 200 characters"`
	Required         bool     `json:"required,omitempty"`
	Options          []Option `json:"options,omitempty" jsonschema:"choices for SELECT fields"`
	AllowMultiSelect bool     `json:"allowMultiSelect,omitempty"`
}

type AuthField struct {
	Slug        string `json:"slug" jsonschema:"unique identifier within the integration, max 100 characters; referenced in action code as data.auth.<slug>"`
	Label       string `json:"label" jsonschema:"display label, max 100 characters"`
	Type        string `json:"type" jsonschema:"input type"`
	Description string `json:"description,omitempty" jsonschema:"help text, max 500 characters"`
	Placeholder string `json:"placeholder,omitempty" jsonschema:"max 200 characters"`
	Required    bool   `json:"required,omitempty"`
}

type OAuthClient struct {
	Scopes            string `json:"scopes,omitempty" jsonschema:"space-separated OAuth scopes"`
	AuthURL           string `json:"authUrl,omitempty" jsonschema:"authorization endpoint URL"`
	TokenURL          string `json:"tokenUrl,omitempty" jsonschema:"token endpoint URL"`
	ClientID          string `json:"clientId,omitempty"`
	ClientSecret      string `json:"clientSecret,omitempty" jsonschema:"write-only, never returned by the API"`
	Label             string `json:"label,omitempty" jsonschema:"label of the connect button"`
	AuthorizationCode string `json:"authorizationCode,omitempty" jsonschema:"custom authorization code (JavaScript), max 1000 characters"`
	AccessTokenCode   string `json:"accessTokenCode,omitempty" jsonschema:"custom access token code (JavaScript), max 1000 characters"`
	RefreshTokenCode  string `json:"refreshTokenCode,omitempty" jsonschema:"custom refresh token code (JavaScript), max 1000 characters"`
}

type NoInput struct{}

type IntegrationRef struct {
	IntegrationID string `json:"integrationId" jsonschema:"UUID of the integration"`
}

type CreateIntegrationInput struct {
	Name        string `json:"name" jsonschema:"integration name, max 40 characters"`
	Description string `json:"description,omitempty" jsonschema:"max 90 characters"`
}

type UpdateIntegrationInput struct {
	IntegrationID string  `json:"integrationId" jsonschema:"UUID of the integration"`
	Name          *string `json:"name,omitempty" jsonschema:"max 40 characters"`
	Description   *string `json:"description,omitempty" jsonschema:"max 90 characters"`
	IconURL       *string `json:"iconUrl,omitempty" jsonschema:"URL of the integration icon"`
	Color         *string `json:"color,omitempty" jsonschema:"hex color code, e.g. #1A73E8"`
}

type UpdateAuthInput struct {
	IntegrationID string       `json:"integrationId" jsonschema:"UUID of the integration"`
	AuthType      string       `json:"authType" jsonschema:"authentication method"`
	AuthFields    []AuthField  `json:"authFields,omitempty" jsonschema:"credential inputs users fill in when connecting (for API_KEY)"`
	AuthTestCode  string       `json:"authTestCode,omitempty" jsonschema:"JavaScript that validates the credentials, max 1000 characters"`
	OAuthClient   *OAuthClient `json:"oauthClient,omitempty" jsonschema:"only for OAUTH / OAUTH_DCR"`
}

type ReplaceIconInput struct {
	IntegrationID string `json:"integrationId" jsonschema:"UUID of the integration"`
	FilePath      string `json:"filePath" jsonschema:"absolute path to a local PNG, JPEG or WebP image on the machine running this server"`
}

type CreateActionInput struct {
	IntegrationID        string       `json:"integrationId" jsonschema:"UUID of the integration"`
	Name                 string       `json:"name" jsonschema:"action name, max 100 characters, unique within the integration"`
	Description          string       `json:"description,omitempty" jsonschema:"tells agents when to use the action, max 1000 characters"`
	Code                 string       `json:"code,omitempty" jsonschema:"async JavaScript run in Langdock's sandbox with data.input, data.auth, ld.request and ld.log; max 150000 characters"`
	InputFields          []InputField `json:"inputFields,omitempty"`
	RequiresConfirmation *bool        `json:"requiresConfirmation,omitempty" jsonschema:"ask the user before running, default true"`
}

type UpdateActionInput struct {
	IntegrationID        string        `json:"integrationId" jsonschema:"UUID of the integration"`
	ActionID             string        `json:"actionId" jsonschema:"UUID of the action"`
	Name                 *string       `json:"name,omitempty" jsonschema:"new name, max 100 characters; the slug does not change"`
	Description          *string       `json:"description,omitempty" jsonschema:"new description, max 1000 characters"`
	Code                 *string       `json:"code,omitempty" jsonschema:"new JavaScript code, max 150000 characters"`
	InputFields          *[]InputField `json:"inputFields,omitempty" jsonschema:"replaces the complete input field list"`
	RequiresConfirmation *bool         `json:"requiresConfirmation,omitempty"`
	ClearDescription     bool          `json:"clearDescription,omitempty" jsonschema:"set to remove the description"`
	ClearInputFields     bool          `json:"clearInputFields,omitempty" jsonschema:"set to remove all input fields"`
}

type ActionRef struct {
	IntegrationID string `json:"integrationId" jsonschema:"UUID of the integration"`
	ActionID      string `json:"actionId" jsonschema:"UUID of the action"`
}

type TriggerInput struct {
	IntegrationID string              `json:"integrationId" jsonschema:"UUID of the integration"`
	TriggerID     string              `json:"triggerId,omitempty" jsonschema:"UUID of the trigger (update only)"`
	Name          string              `json:"name" jsonschema:"trigger name, max 100 characters"`
	Description   string              `json:"description,omitempty" jsonschema:"max 90 characters"`
	PollingCode   string              `json:"pollingCode,omitempty" jsonschema:"JavaScript that polls for new events, max 1000 characters"`
	InputFields   []TriggerInputField `json:"inputFields,omitempty" jsonschema:"fields for configuring the trigger"`
}

type TriggerRef struct {
	IntegrationID string `json:"integrationId" jsonschema:"UUID of the integration"`
	TriggerID     string `json:"triggerId" jsonschema:"UUID of the trigger"`
}

// Server exposes the Integrations API as MCP tools.
type Server struct {
	client *Client
	mcp    *mcp.Server
}

func NewServer(client *Client) *Server {
	s := &Server{client: client, mcp: mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, nil)}
	s.register()
	return s
}

func (s *Server) Run(ctx context.Context) error {
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) register() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)}
	additive := &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)}
	// Updates replace existing state, so they're flagged destructive like deletes.
	destructive := &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(true)}

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_integrations",
		Description: "List the private API, MCP and A2A integrations shared with the API key.",
		Annotations: readOnly,
	}, s.listIntegrations)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "get_integration",
		Description: "Get one integration with its auth configuration, actions (including code and input fields) and triggers.",
		Annotations: readOnly,
	}, s.getIntegration)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "create_integration",
		Description: "Create a custom integration. Configure auth with update_integration_auth and add capabilities with create_action / create_trigger.",
		Annotations: additive,
		InputSchema: schemaFor[CreateIntegrationInput](func(sc *jsonschema.Schema) {
			maxLen(sc, 40, "name")
			maxLen(sc, 90, "description")
		}),
	}, s.createIntegration)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_integration",
		Description: "Update an integration's name, description, icon URL or color. Omitted fields stay unchanged.",
		Annotations: destructive,
		InputSchema: schemaFor[UpdateIntegrationInput](func(sc *jsonschema.Schema) {
			maxLen(sc, 40, "name")
			maxLen(sc, 90, "description")
		}),
	}, s.updateIntegration)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_integration_auth",
		Description: "Set an integration's authentication: NONE, API_KEY (with authFields users fill in), OAUTH or OAUTH_DCR (with oauthClient). Replaces the current auth configuration.",
		Annotations: destructive,
		InputSchema: schemaFor[UpdateAuthInput](func(sc *jsonschema.Schema) {
			enum(sc, authTypes, "authType")
			enum(sc, authFieldTypes, "authFields", "[]", "type")
			maxLen(sc, 1000, "authTestCode")
		}),
	}, s.updateAuth)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "get_integration_icon",
		Description: "Get the URL of an integration's icon.",
		Annotations: readOnly,
	}, s.getIcon)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "replace_integration_icon",
		Description: "Upload a local image file (PNG, JPEG or WebP, max 20 MB) as the integration's icon.",
		Annotations: destructive,
	}, s.replaceIcon)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "create_action",
		Description: "Add an action to an integration. The code is an async JavaScript function body run in Langdock's sandbox; it reads inputs from data.input.<field slug>, credentials from data.auth.<slug>, calls APIs via ld.request and should handle errors itself.",
		Annotations: additive,
		InputSchema: schemaFor[CreateActionInput](func(sc *jsonschema.Schema) {
			actionSchema(sc)
		}),
	}, s.createAction)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_action",
		Description: "Update an action. Only the fields you pass change; everything else (including description and input fields) is kept. inputFields, when passed, replaces the whole list. Use clearDescription / clearInputFields to remove them.",
		Annotations: destructive,
		InputSchema: schemaFor[UpdateActionInput](func(sc *jsonschema.Schema) {
			actionSchema(sc)
		}),
	}, s.updateAction)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "delete_action",
		Description: "Permanently delete an action from an integration.",
		Annotations: destructive,
	}, s.deleteAction)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "create_trigger",
		Description: "Add a polling trigger to an integration.",
		Annotations: additive,
		InputSchema: schemaFor[TriggerInput](triggerSchema),
	}, s.createTrigger)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_trigger",
		Description: "Replace a trigger's definition. Unlike update_action this is a full replace: pass every field you want to keep (get_integration does not return pollingCode or trigger input fields).",
		Annotations: destructive,
		InputSchema: schemaFor[TriggerInput](func(sc *jsonschema.Schema) {
			triggerSchema(sc)
			sc.Required = append(sc.Required, "triggerId")
		}),
	}, s.updateTrigger)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "delete_trigger",
		Description: "Permanently delete a trigger from an integration.",
		Annotations: destructive,
	}, s.deleteTrigger)
}

func actionSchema(sc *jsonschema.Schema) {
	maxLen(sc, 100, "name")
	maxLen(sc, 1000, "description")
	maxLen(sc, 150000, "code")
	enum(sc, actionFieldTypes, "inputFields", "[]", "type")
}

func triggerSchema(sc *jsonschema.Schema) {
	maxLen(sc, 100, "name")
	maxLen(sc, 90, "description")
	maxLen(sc, 1000, "pollingCode")
	enum(sc, triggerFieldTypes, "inputFields", "[]", "type")
}

func (s *Server) listIntegrations(ctx context.Context, _ *mcp.CallToolRequest, _ NoInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodGet, "/integrations/v1/get", nil)
}

func (s *Server) getIntegration(ctx context.Context, _ *mcp.CallToolRequest, in IntegrationRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodGet, integrationPath(in.IntegrationID), nil)
}

func (s *Server) createIntegration(ctx context.Context, _ *mcp.CallToolRequest, in CreateIntegrationInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, "/integrations/v1/create", in)
}

func (s *Server) updateIntegration(ctx context.Context, _ *mcp.CallToolRequest, in UpdateIntegrationInput) (*mcp.CallToolResult, any, error) {
	body := struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
		IconURL     *string `json:"iconUrl,omitempty"`
		Color       *string `json:"color,omitempty"`
	}{in.Name, in.Description, in.IconURL, in.Color}
	return s.call(ctx, http.MethodPatch, integrationPath(in.IntegrationID), body)
}

func (s *Server) updateAuth(ctx context.Context, _ *mcp.CallToolRequest, in UpdateAuthInput) (*mcp.CallToolResult, any, error) {
	body := struct {
		AuthType     string       `json:"authType"`
		AuthFields   []AuthField  `json:"authFields,omitempty"`
		AuthTestCode string       `json:"authTestCode,omitempty"`
		OAuthClient  *OAuthClient `json:"oauthClient,omitempty"`
	}{in.AuthType, in.AuthFields, in.AuthTestCode, in.OAuthClient}
	return s.call(ctx, http.MethodPatch, integrationPath(in.IntegrationID)+"/auth", body)
}

func (s *Server) getIcon(ctx context.Context, _ *mcp.CallToolRequest, in IntegrationRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodGet, integrationPath(in.IntegrationID)+"/icon", nil)
}

func (s *Server) replaceIcon(ctx context.Context, _ *mcp.CallToolRequest, in ReplaceIconInput) (*mcp.CallToolResult, any, error) {
	if !filepath.IsAbs(in.FilePath) {
		return nil, nil, fmt.Errorf("filePath must be absolute, got %q", in.FilePath)
	}
	info, err := os.Stat(in.FilePath)
	if err != nil {
		return nil, nil, err
	}
	if info.Size() > maxIconBytes {
		return nil, nil, fmt.Errorf("icon is %d bytes, limit is 20 MB", info.Size())
	}
	data, err := os.ReadFile(in.FilePath)
	if err != nil {
		return nil, nil, err
	}
	contentType := http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		return nil, nil, fmt.Errorf("%s is not an image (detected %s)", in.FilePath, contentType)
	}
	body, err := s.client.uploadFile(ctx, http.MethodPut, integrationPath(in.IntegrationID)+"/icon", "icon", filepath.Base(in.FilePath), contentType, data)
	if err != nil {
		return nil, nil, err
	}
	return textResult(body), nil, nil
}

func (s *Server) createAction(ctx context.Context, _ *mcp.CallToolRequest, in CreateActionInput) (*mcp.CallToolResult, any, error) {
	body := struct {
		Name                 string       `json:"name"`
		Description          string       `json:"description,omitempty"`
		Code                 string       `json:"code,omitempty"`
		InputFields          []InputField `json:"inputFields,omitempty"`
		RequiresConfirmation *bool        `json:"requiresConfirmation,omitempty"`
	}{in.Name, in.Description, in.Code, in.InputFields, in.RequiresConfirmation}
	return s.call(ctx, http.MethodPost, integrationPath(in.IntegrationID)+"/actions/create", body)
}

// currentAction is an action as returned inside get_integration.
type currentAction struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	InputFields []InputField `json:"inputFields"`
}

type actionUpdateBody struct {
	Name                 string       `json:"name"`
	Description          string       `json:"description,omitempty"`
	Code                 *string      `json:"code,omitempty"`
	InputFields          []InputField `json:"inputFields"`
	RequiresConfirmation *bool        `json:"requiresConfirmation,omitempty"`
}

// updateAction turns the API's replace semantics into a patch: the PUT
// endpoint clears description and drops every input field that isn't
// resent, which would silently destroy an action when a caller only meant
// to change its code. code and requiresConfirmation are preserved by the
// API itself when omitted, so they're only sent when given.
func (s *Server) updateAction(ctx context.Context, _ *mcp.CallToolRequest, in UpdateActionInput) (*mcp.CallToolResult, any, error) {
	raw, err := s.client.doJSON(ctx, http.MethodGet, integrationPath(in.IntegrationID), nil)
	if err != nil {
		return nil, nil, err
	}
	var env struct {
		Integration struct {
			Actions []currentAction `json:"actions"`
		} `json:"integration"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, nil, fmt.Errorf("decoding integration: %w", err)
	}
	var cur *currentAction
	for i := range env.Integration.Actions {
		if env.Integration.Actions[i].ID == in.ActionID {
			cur = &env.Integration.Actions[i]
			break
		}
	}
	if cur == nil {
		return nil, nil, fmt.Errorf("action %s not found in integration %s", in.ActionID, in.IntegrationID)
	}
	body, err := mergeAction(*cur, in)
	if err != nil {
		return nil, nil, err
	}
	return s.call(ctx, http.MethodPut, integrationPath(in.IntegrationID)+"/actions/"+url.PathEscape(in.ActionID), body)
}

func mergeAction(cur currentAction, in UpdateActionInput) (actionUpdateBody, error) {
	if in.ClearDescription && in.Description != nil {
		return actionUpdateBody{}, fmt.Errorf("pass either description or clearDescription, not both")
	}
	if in.ClearInputFields && in.InputFields != nil {
		return actionUpdateBody{}, fmt.Errorf("pass either inputFields or clearInputFields, not both")
	}
	b := actionUpdateBody{
		Name:                 cur.Name,
		Description:          cur.Description,
		Code:                 in.Code,
		InputFields:          cur.InputFields,
		RequiresConfirmation: in.RequiresConfirmation,
	}
	if in.Name != nil {
		b.Name = *in.Name
	}
	switch {
	case in.ClearDescription:
		b.Description = ""
	case in.Description != nil:
		b.Description = *in.Description
	}
	switch {
	case in.ClearInputFields:
		b.InputFields = nil
	case in.InputFields != nil:
		b.InputFields = *in.InputFields
	}
	if b.InputFields == nil {
		b.InputFields = []InputField{}
	}
	return b, nil
}

func (s *Server) deleteAction(ctx context.Context, _ *mcp.CallToolRequest, in ActionRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodDelete, integrationPath(in.IntegrationID)+"/actions/"+url.PathEscape(in.ActionID), nil)
}

type triggerBody struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	PollingCode string              `json:"pollingCode,omitempty"`
	InputFields []TriggerInputField `json:"inputFields,omitempty"`
}

func newTriggerBody(in TriggerInput) triggerBody {
	return triggerBody{in.Name, in.Description, in.PollingCode, in.InputFields}
}

func (s *Server) createTrigger(ctx context.Context, _ *mcp.CallToolRequest, in TriggerInput) (*mcp.CallToolResult, any, error) {
	if in.TriggerID != "" {
		return nil, nil, fmt.Errorf("triggerId is only valid for update_trigger")
	}
	return s.call(ctx, http.MethodPost, integrationPath(in.IntegrationID)+"/triggers/create", newTriggerBody(in))
}

func (s *Server) updateTrigger(ctx context.Context, _ *mcp.CallToolRequest, in TriggerInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPut, integrationPath(in.IntegrationID)+"/triggers/"+url.PathEscape(in.TriggerID), newTriggerBody(in))
}

func (s *Server) deleteTrigger(ctx context.Context, _ *mcp.CallToolRequest, in TriggerRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodDelete, integrationPath(in.IntegrationID)+"/triggers/"+url.PathEscape(in.TriggerID), nil)
}

// call performs a JSON request and returns the response body as the tool
// result. A returned error becomes a tool-level error (IsError) that the
// model can read and react to, not a protocol failure.
func (s *Server) call(ctx context.Context, method, path string, body any) (*mcp.CallToolResult, any, error) {
	resp, err := s.client.doJSON(ctx, method, path, body)
	if err != nil {
		return nil, nil, err
	}
	return textResult(resp), nil, nil
}

func integrationPath(id string) string {
	return "/integrations/v1/" + url.PathEscape(id)
}

func textResult(body []byte) *mcp.CallToolResult {
	text := strings.TrimSpace(string(body))
	if text == "" {
		text = `{"success":true}`
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func ptr[T any](v T) *T { return &v }

// schemaFor infers T's input schema and lets the caller add constraints the
// struct tags can't express (enums, length limits), so the SDK rejects bad
// input before it reaches Langdock.
func schemaFor[T any](patch func(*jsonschema.Schema)) *jsonschema.Schema {
	sc, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	patch(sc)
	return sc
}

// at walks a schema by property names; "[]" steps into array items.
func at(sc *jsonschema.Schema, path ...string) *jsonschema.Schema {
	for _, p := range path {
		if p == "[]" {
			sc = sc.Items
		} else {
			sc = sc.Properties[p]
		}
		if sc == nil {
			panic(fmt.Sprintf("schema path %v not found", path))
		}
	}
	return sc
}

func enum(sc *jsonschema.Schema, values []string, path ...string) {
	target := at(sc, path...)
	target.Enum = make([]any, len(values))
	for i, v := range values {
		target.Enum[i] = v
	}
}

func maxLen(sc *jsonschema.Schema, n int, path ...string) {
	at(sc, path...).MaxLength = ptr(n)
}
