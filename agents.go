package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	agentInputTypes      = []string{"PROMPT", "STRUCTURED", "INTEGRATION", "SCHEDULED", "WEBHOOK"}
	agentInputFieldTypes = []string{"TEXT", "MULTI_LINE_TEXT", "NUMBER", "CHECKBOX", "FILE", "SELECT", "MULTI_SELECT", "DATE", "EMAIL"}
)

type AgentAction struct {
	ActionID             string `json:"actionId" jsonschema:"UUID of an action from an integration enabled in the workspace"`
	RequiresConfirmation *bool  `json:"requiresConfirmation,omitempty" jsonschema:"ask the user before running, default true"`
}

type AgentInputField struct {
	Slug        string   `json:"slug" jsonschema:"unique identifier, max 64 characters"`
	Type        string   `json:"type" jsonschema:"field type"`
	Label       string   `json:"label" jsonschema:"display label, max 255 characters"`
	Description string   `json:"description,omitempty" jsonschema:"help text, max 512 characters"`
	Required    bool     `json:"required,omitempty"`
	Order       int      `json:"order" jsonschema:"display order, 0-indexed"`
	Options     []string `json:"options,omitempty" jsonschema:"choices for SELECT and MULTI_SELECT fields"`
	FileTypes   *string  `json:"fileTypes,omitempty" jsonschema:"allowed file types for FILE fields"`
	EmailDomain *string  `json:"emailDomain,omitempty" jsonschema:"allowed email domain for EMAIL fields"`
}

// AgentSettings are the writable agent fields shared by create and update.
// Pointers keep "omitted" distinct from zero values, because the update
// endpoint leaves omitted fields unchanged and replaces passed arrays.
type AgentSettings struct {
	Name                 *string            `json:"name,omitempty" jsonschema:"1-80 characters"`
	Description          *string            `json:"description,omitempty" jsonschema:"max 800 characters; empty string clears it on update"`
	Emoji                *string            `json:"emoji,omitempty" jsonschema:"emoji icon, max 16 characters"`
	Instruction          *string            `json:"instruction,omitempty" jsonschema:"system prompt, max 50000 characters; empty string clears it on update"`
	InputType            *string            `json:"inputType,omitempty" jsonschema:"default PROMPT"`
	Model                *string            `json:"model,omitempty" jsonschema:"id from list_agent_models"`
	Creativity           *float64           `json:"creativity,omitempty" jsonschema:"temperature between 0 and 1"`
	ConversationStarters *[]string          `json:"conversationStarters,omitempty" jsonschema:"suggested prompts, max 20, each 1-255 characters; replaces the whole list"`
	Actions              *[]AgentAction     `json:"actions,omitempty" jsonschema:"replaces the whole action list; prefer add_agent_actions / remove_agent_actions"`
	InputFields          *[]AgentInputField `json:"inputFields,omitempty" jsonschema:"form fields for STRUCTURED input; replaces the whole list"`
	Attachments          *[]string          `json:"attachments,omitempty" jsonschema:"up to 50 attachment UUIDs; replaces the whole list"`
	KnowledgeFolderIDs   *[]string          `json:"knowledgeFolderIds,omitempty" jsonschema:"knowledge folder UUIDs; replaces the whole list"`
	WebSearch            *bool              `json:"webSearch,omitempty"`
	ImageGeneration      *bool              `json:"imageGeneration,omitempty"`
	ExtendedThinking     *bool              `json:"extendedThinking,omitempty" jsonschema:"only on models that support it"`
}

// PublishOption lets a write and its publication be one tool call.
type PublishOption struct {
	Publish            bool   `json:"publish,omitempty" jsonschema:"publish the draft as a new version right after the change"`
	PublishDescription string `json:"publishDescription,omitempty" jsonschema:"change description shown in version history when publish is set"`
}

type AgentRef struct {
	AgentID string `json:"agentId" jsonschema:"UUID of the agent"`
}

type CreateAgentInput struct {
	AgentSettings
	PublishOption
}

type UpdateAgentInput struct {
	AgentID string `json:"agentId" jsonschema:"UUID of the agent"`
	AgentSettings
	ClearEmoji bool `json:"clearEmoji,omitempty" jsonschema:"set to remove the emoji icon"`
	PublishOption
}

type PublishAgentInput struct {
	AgentID     string `json:"agentId" jsonschema:"UUID of the agent"`
	Description string `json:"description,omitempty" jsonschema:"change description shown in version history"`
}

type AddAgentActionsInput struct {
	AgentID string        `json:"agentId" jsonschema:"UUID of the agent"`
	Actions []AgentAction `json:"actions" jsonschema:"actions to add; an action already on the agent only gets its requiresConfirmation updated when given"`
	PublishOption
}

type RemoveAgentActionsInput struct {
	AgentID   string   `json:"agentId" jsonschema:"UUID of the agent"`
	ActionIDs []string `json:"actionIds" jsonschema:"UUIDs of the actions to remove"`
	PublishOption
}

type DisableAgentInput struct {
	AgentID  string `json:"agentId" jsonschema:"UUID of the agent"`
	Disabled bool   `json:"disabled" jsonschema:"true disables the agent, false enables it again"`
}

type AgentMessageMetadata struct {
	Attachments []string `json:"attachments,omitempty" jsonschema:"attachment UUIDs from upload_attachment"`
}

type AgentMessage struct {
	ID   string `json:"id,omitempty" jsonschema:"unique message id; generated when omitted"`
	Role string `json:"role"`
	// Parts stay open maps, because assistant turns from earlier replies carry
	// reasoning, tool-* and source-* parts that must be resent unchanged.
	Parts    []map[string]any      `json:"parts" jsonschema:"content parts; user parts are {type: text, text} or {type: file, mediaType, url, filename?}; resend parts of earlier assistant replies unchanged"`
	Metadata *AgentMessageMetadata `json:"metadata,omitempty"`
}

type AgentOutput struct {
	Type   string         `json:"type" jsonschema:"shape of the structured output"`
	Schema map[string]any `json:"schema,omitempty" jsonschema:"JSON Schema for object and array output"`
	Enum   []string       `json:"enum,omitempty" jsonschema:"allowed values for enum output"`
}

type ChatWithAgentInput struct {
	AgentID  string         `json:"agentId" jsonschema:"UUID of an agent shared with the API key"`
	Messages []AgentMessage `json:"messages" jsonschema:"conversation so far in Vercel AI SDK UIMessage format, ending with the user's message; resend earlier turns for follow-ups, because the API keeps no conversation state"`
	Output   *AgentOutput   `json:"output,omitempty" jsonschema:"request structured output, returned in the response's output field"`
	MaxSteps int            `json:"maxSteps,omitempty" jsonschema:"maximum tool execution steps, 1-20"`
	// ImageResponseFormat matters because b64_json images can push the reply
	// past the 10 MB response limit.
	ImageResponseFormat string `json:"imageResponseFormat,omitempty" jsonschema:"format of agent-generated images; prefer url, because b64_json can exceed the 10 MB response limit"`
}

const draftWarning = " The current list is read via get_agent, which returns the published version, so action changes that exist only in the draft are overwritten."

func (s *Server) registerAgents() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)}
	additive := &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)}
	destructive := &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)}

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "get_agent",
		Description: "Get an agent's settings, actions, input fields, attachments and owner. Returns the published version, or the draft if the agent was never published. The API cannot list agents or change sharing; both are only possible in the Langdock UI.",
		Annotations: readOnly,
	}, s.getAgent)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_agent_models",
		Description: "List the models available to agents. Pass a model's id as model to create_agent / update_agent.",
		Annotations: readOnly,
	}, s.listAgentModels)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "create_agent",
		Description: "Create an agent owned by the API key's owner and shared with the key. model is required because the workspace may have no default model. The agent starts as a draft; set publish to make it live in the same call.",
		Annotations: additive,
		InputSchema: schemaFor[CreateAgentInput](func(sc *jsonschema.Schema) {
			agentSchema(sc)
			sc.Required = append(sc.Required, "name", "model")
		}),
	}, s.createAgent)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_agent",
		Description: "Update an agent's draft. Only the fields you pass change. Array fields (conversationStarters, actions, inputFields, attachments, knowledgeFolderIds) replace the whole list when passed, and [] empties it; use add_agent_actions / remove_agent_actions to change single actions. Empty strings clear description and instruction, clearEmoji removes the emoji. Changes stay invisible to users until the draft is published (publish: true, or publish_agent).",
		Annotations: destructive,
		InputSchema: schemaFor[UpdateAgentInput](agentSchema),
	}, s.updateAgent)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "publish_agent",
		Description: "Publish an agent's draft as a new version that users see. Fails with 409 when the draft has no changes.",
		Annotations: destructive,
		InputSchema: schemaFor[PublishAgentInput](func(sc *jsonschema.Schema) {
			maxLen(sc, 500, "description")
		}),
	}, s.publishAgent)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "add_agent_actions",
		Description: "Add actions to an agent's draft and keep its existing actions." + draftWarning,
		Annotations: destructive,
		InputSchema: schemaFor[AddAgentActionsInput](func(sc *jsonschema.Schema) {
			publishSchema(sc)
			at(sc, "actions").MinItems = ptr(1)
		}),
	}, s.addAgentActions)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "remove_agent_actions",
		Description: "Remove actions from an agent's draft and keep all others. Fails without writing if an id is not on the agent." + draftWarning,
		Annotations: destructive,
		InputSchema: schemaFor[RemoveAgentActionsInput](func(sc *jsonschema.Schema) {
			publishSchema(sc)
			at(sc, "actionIds").MinItems = ptr(1)
		}),
	}, s.removeAgentActions)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "disable_agent",
		Description: "Disable an agent (disabled: true) so users can no longer chat with it, or enable it again (disabled: false). Needs workspace admin rights or the matching permission.",
		Annotations: destructive,
		InputSchema: schemaFor[DisableAgentInput](func(sc *jsonschema.Schema) {
			at(sc, "agentId").Pattern = uuidPattern
		}),
	}, s.disableAgent)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "chat_with_agent",
		Description: "Send a conversation to an agent and return its reply (non-streaming). The agent runs with its configured model, knowledge and actions, so a reply can trigger the agent's actions in connected systems. Langdock aborts requests that take longer than 100 seconds. Attach files with upload_attachment and the message's metadata.attachments.",
		Annotations: &mcp.ToolAnnotations{OpenWorldHint: ptr(true)},
		InputSchema: schemaFor[ChatWithAgentInput](func(sc *jsonschema.Schema) {
			at(sc, "agentId").Pattern = uuidPattern
			// Inferred slices also allow null, which would reach the API as [].
			for _, p := range []*jsonschema.Schema{at(sc, "messages"), at(sc, "messages", "[]", "parts")} {
				p.Type, p.Types, p.MinItems = "array", nil, ptr(1)
			}
			enum(sc, []string{"system", "user", "assistant"}, "messages", "[]", "role")
			at(sc, "messages", "[]", "parts", "[]").Required = []string{"type"}
			at(sc, "messages", "[]", "metadata", "attachments", "[]").Pattern = uuidPattern
			enum(sc, []string{"object", "array", "enum"}, "output", "type")
			at(sc, "maxSteps").Minimum = ptr(1.0)
			at(sc, "maxSteps").Maximum = ptr(20.0)
			enum(sc, []string{"url", "b64_json"}, "imageResponseFormat")
		}),
	}, s.chatWithAgent)
}

func agentSchema(sc *jsonschema.Schema) {
	at(sc, "name").MinLength = ptr(1)
	maxLen(sc, 80, "name")
	maxLen(sc, 800, "description")
	maxLen(sc, 16, "emoji")
	maxLen(sc, 50000, "instruction")
	enum(sc, agentInputTypes, "inputType")
	at(sc, "creativity").Minimum = ptr(0.0)
	at(sc, "creativity").Maximum = ptr(1.0)
	at(sc, "conversationStarters").MaxItems = ptr(20)
	at(sc, "conversationStarters", "[]").MinLength = ptr(1)
	maxLen(sc, 255, "conversationStarters", "[]")
	at(sc, "attachments").MaxItems = ptr(50)
	enum(sc, agentInputFieldTypes, "inputFields", "[]", "type")
	maxLen(sc, 64, "inputFields", "[]", "slug")
	maxLen(sc, 255, "inputFields", "[]", "label")
	maxLen(sc, 512, "inputFields", "[]", "description")
	maxLen(sc, 255, "inputFields", "[]", "options", "[]")
	maxLen(sc, 255, "inputFields", "[]", "fileTypes")
	publishSchema(sc)
}

// The prose docs say 100 characters, the OpenAPI schema 500; the larger
// limit avoids rejecting input Langdock would accept.
func publishSchema(sc *jsonschema.Schema) {
	maxLen(sc, 500, "publishDescription")
}

func (s *Server) disableAgent(ctx context.Context, _ *mcp.CallToolRequest, in DisableAgentInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPatch, "/agent/v1/disable", in)
}

func (s *Server) chatWithAgent(ctx context.Context, _ *mcp.CallToolRequest, in ChatWithAgentInput) (*mcp.CallToolResult, any, error) {
	msgs := make([]AgentMessage, len(in.Messages))
	for i, m := range in.Messages {
		if m.ID == "" {
			m.ID = randomMessageID()
		}
		msgs[i] = m
	}
	body := map[string]any{"agentId": in.AgentID, "messages": msgs, "stream": false}
	if in.Output != nil {
		body["output"] = in.Output
	}
	if in.MaxSteps != 0 {
		body["maxSteps"] = in.MaxSteps
	}
	if in.ImageResponseFormat != "" {
		body["imageResponseFormat"] = in.ImageResponseFormat
	}
	// Completions may legitimately run up to Langdock's 100-second limit,
	// beyond the default client timeout, but not for the upload client's ten
	// minutes.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	raw, err := s.client.doJSONSlow(ctx, http.MethodPost, "/agent/v1/chat/completions", body)
	if err != nil {
		return nil, nil, err
	}
	return textResult(raw), nil, nil
}

// randomMessageID avoids colliding with ids the caller chose for other turns.
func randomMessageID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "msg-" + hex.EncodeToString(b)
}

func (s *Server) getAgent(ctx context.Context, _ *mcp.CallToolRequest, in AgentRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodGet, agentGetPath(in.AgentID), nil)
}

func (s *Server) listAgentModels(ctx context.Context, _ *mcp.CallToolRequest, _ NoInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodGet, "/agent/v1/models", nil)
}

func (s *Server) createAgent(ctx context.Context, _ *mcp.CallToolRequest, in CreateAgentInput) (*mcp.CallToolResult, any, error) {
	raw, err := s.client.doJSON(ctx, http.MethodPost, "/agent/v1/create", in.AgentSettings)
	if err != nil {
		return nil, nil, err
	}
	if !in.Publish {
		return textResult(raw), nil, nil
	}
	var created struct {
		Agent struct {
			ID string `json:"id"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.Agent.ID == "" {
		return nil, nil, fmt.Errorf("agent created, but its id could not be read to publish it: %s", raw)
	}
	return s.publishAfter(ctx, raw, created.Agent.ID, in.PublishOption)
}

func (s *Server) updateAgent(ctx context.Context, _ *mcp.CallToolRequest, in UpdateAgentInput) (*mcp.CallToolResult, any, error) {
	if in.ClearEmoji && in.Emoji != nil {
		return nil, nil, fmt.Errorf("pass either emoji or clearEmoji, not both")
	}
	body, err := agentUpdateBody(in.AgentID, in.AgentSettings)
	if err != nil {
		return nil, nil, err
	}
	if in.ClearEmoji {
		body["emoji"] = nil
	}
	if len(body) == 1 {
		return nil, nil, fmt.Errorf("nothing to update: pass at least one field")
	}
	return s.patchAgent(ctx, in.AgentID, body, in.PublishOption)
}

// agentUpdateBody flattens settings into a map so fields can be set to an
// explicit null, which the struct's omitempty pointers cannot express.
func agentUpdateBody(agentID string, settings any) (map[string]any, error) {
	b, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, err
	}
	body["agentId"] = agentID
	return body, nil
}

func (s *Server) publishAgent(ctx context.Context, _ *mcp.CallToolRequest, in PublishAgentInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, "/agent/v1/publish", in)
}

func (s *Server) addAgentActions(ctx context.Context, _ *mcp.CallToolRequest, in AddAgentActionsInput) (*mcp.CallToolResult, any, error) {
	cur, err := s.currentAgentActions(ctx, in.AgentID)
	if err != nil {
		return nil, nil, err
	}
	merged, err := addActions(cur, in.Actions)
	if err != nil {
		return nil, nil, err
	}
	return s.patchAgent(ctx, in.AgentID, map[string]any{"agentId": in.AgentID, "actions": merged}, in.PublishOption)
}

func (s *Server) removeAgentActions(ctx context.Context, _ *mcp.CallToolRequest, in RemoveAgentActionsInput) (*mcp.CallToolResult, any, error) {
	cur, err := s.currentAgentActions(ctx, in.AgentID)
	if err != nil {
		return nil, nil, err
	}
	kept, err := removeActions(cur, in.ActionIDs)
	if err != nil {
		return nil, nil, err
	}
	return s.patchAgent(ctx, in.AgentID, map[string]any{"agentId": in.AgentID, "actions": kept}, in.PublishOption)
}

// agentActionEntry keeps an action entry's raw properties, so fields the
// docs don't describe (such as connectionId) survive the round trip.
type agentActionEntry map[string]json.RawMessage

func (e agentActionEntry) id() string {
	var id string
	json.Unmarshal(e["actionId"], &id)
	return id
}

func (s *Server) currentAgentActions(ctx context.Context, agentID string) ([]agentActionEntry, error) {
	raw, err := s.client.doJSON(ctx, http.MethodGet, agentGetPath(agentID), nil)
	if err != nil {
		return nil, err
	}
	var env struct {
		Agent *struct {
			Actions []agentActionEntry `json:"actions"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decoding agent: %w", err)
	}
	if env.Agent == nil {
		return nil, fmt.Errorf("response has no agent: %s", raw)
	}
	// get_agent returns "connectionId": null on every action; dropping nulls
	// keeps the resent entries within the documented request shape.
	for _, e := range env.Agent.Actions {
		for k, v := range e {
			if string(v) == "null" {
				delete(e, k)
			}
		}
	}
	return env.Agent.Actions, nil
}

func addActions(cur []agentActionEntry, add []AgentAction) ([]agentActionEntry, error) {
	out := append([]agentActionEntry{}, cur...)
	index := map[string]int{}
	for i, e := range out {
		index[e.id()] = i
	}
	for _, a := range add {
		if a.ActionID == "" {
			return nil, fmt.Errorf("actionId must not be empty")
		}
		i, ok := index[a.ActionID]
		if !ok {
			out = append(out, agentActionEntry{})
			i = len(out) - 1
			index[a.ActionID] = i
			out[i]["actionId"], _ = json.Marshal(a.ActionID)
		}
		if a.RequiresConfirmation != nil {
			out[i]["requiresConfirmation"], _ = json.Marshal(*a.RequiresConfirmation)
		}
	}
	return out, nil
}

func removeActions(cur []agentActionEntry, ids []string) ([]agentActionEntry, error) {
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	kept := []agentActionEntry{}
	for _, e := range cur {
		if drop[e.id()] {
			delete(drop, e.id())
			continue
		}
		kept = append(kept, e)
	}
	if len(drop) > 0 {
		var missing []string
		for _, id := range ids {
			if drop[id] {
				missing = append(missing, id)
			}
		}
		return nil, fmt.Errorf("actions not on the agent, nothing changed: %s", strings.Join(missing, ", "))
	}
	return kept, nil
}

func (s *Server) patchAgent(ctx context.Context, agentID string, body any, opt PublishOption) (*mcp.CallToolResult, any, error) {
	raw, err := s.client.doJSON(ctx, http.MethodPatch, "/agent/v1/update", body)
	if err != nil {
		return nil, nil, err
	}
	if !opt.Publish {
		return textResult(raw), nil, nil
	}
	return s.publishAfter(ctx, raw, agentID, opt)
}

// publishAfter publishes the draft a previous write produced and returns both
// responses, so the caller sees the written agent and the new version.
func (s *Server) publishAfter(ctx context.Context, written []byte, agentID string, opt PublishOption) (*mcp.CallToolResult, any, error) {
	pub, err := s.client.doJSON(ctx, http.MethodPost, "/agent/v1/publish", PublishAgentInput{AgentID: agentID, Description: opt.PublishDescription})
	if err != nil {
		return nil, nil, fmt.Errorf("the draft of agent %s was saved, but publishing failed: %w", agentID, err)
	}
	out, err := json.Marshal(struct {
		Draft   json.RawMessage `json:"draft"`
		Publish json.RawMessage `json:"publish"`
	}{rawOrEmpty(written), rawOrEmpty(pub)})
	if err != nil {
		return nil, nil, err
	}
	return textResult(out), nil, nil
}

func rawOrEmpty(b []byte) json.RawMessage {
	if !json.Valid(b) {
		b, _ = json.Marshal(string(b))
	}
	return b
}

func agentGetPath(id string) string {
	return "/agent/v1/get?agentId=" + url.QueryEscape(id)
}
