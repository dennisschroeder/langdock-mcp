package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// DefaultBaseURL is Langdock's public cloud API. Dedicated deployments use
// https://<domain>/api/public instead (LANGDOCK_BASE_URL).
const DefaultBaseURL = "https://api.langdock.com"

// Client is a thin wrapper around Langdock's Integrations, Agents and
// Knowledge Folder APIs. It returns response bodies verbatim so tools can hand
// them to the model unchanged.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	// upload has a longer timeout because knowledge files may be up to 256 MB.
	upload *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
		upload:  &http.Client{Timeout: 10 * time.Minute},
	}
}

// APIError is a non-2xx response from Langdock.
type APIError struct {
	Status int
	Body   string
	// API selects the status hints, because the same code means something
	// different in each API.
	API apiFamily
}

type apiFamily int

const (
	integrationsAPI apiFamily = iota
	agentsAPI
	knowledgeAPI
	usersAPI
)

// familyOf classifies a request path relative to the base URL.
func familyOf(path string) apiFamily {
	p, _, _ := strings.Cut(path, "?")
	switch {
	case strings.HasPrefix(p, "/agent/v1/"):
		return agentsAPI
	case p == "/knowledge" || strings.HasPrefix(p, "/knowledge/"):
		return knowledgeAPI
	case strings.HasPrefix(p, "/user-management/"):
		return usersAPI
	}
	return integrationsAPI
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("Langdock API returned %d", e.Status)
	hint := statusHint(e.Status)
	switch e.API {
	case agentsAPI:
		hint = agentStatusHint(e.Status)
	case knowledgeAPI:
		hint = knowledgeStatusHint(e.Status)
	case usersAPI:
		hint = userStatusHint(e.Status)
	}
	if hint != "" {
		msg += " (" + hint + ")"
	}
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

// statusHint turns the documented status codes into something the model can
// act on without having to know the Integrations API docs.
func statusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid request or ID format"
	case http.StatusUnauthorized:
		return "invalid or missing API key"
	case http.StatusForbidden:
		return "API key lacks the INTEGRATION_API scope or has no access to this integration"
	case http.StatusNotFound:
		return "not found"
	case http.StatusConflict:
		return "an item with this name already exists"
	case http.StatusTooManyRequests:
		return "rate limit of 500 requests/minute exceeded, retry later"
	}
	return ""
}

// agentStatusHint covers the Agents API, where 401/403 typically mean the key
// was created without the Agent API scope.
func agentStatusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid parameters, or a referenced model, action, attachment or folder does not exist in the workspace"
	case http.StatusUnauthorized:
		return "invalid or missing API key, or the key lacks the Agent API scope"
	case http.StatusForbidden:
		return "API key lacks the Agent API scope or has no (edit) access to this agent"
	case http.StatusNotFound:
		return "agent not found or not shared with the API key"
	case http.StatusConflict:
		return "the draft has no changes to publish"
	case http.StatusTooManyRequests:
		return "rate limit exceeded, retry later"
	}
	return ""
}

// knowledgeStatusHint covers the Knowledge Folder API, where write operations
// additionally need the Editor role on the knowledge base.
func knowledgeStatusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid request, file validation failure, ineligible share target, or a change to the owner's access"
	case http.StatusUnauthorized:
		return "invalid or missing API key"
	case http.StatusForbidden:
		return "API key lacks the KNOWLEDGE_FOLDER_API scope, the knowledge base is not shared with it, or writes need the Editor role"
	case http.StatusNotFound:
		return "knowledge base, file or access target not found, or not shared with the API key"
	case http.StatusRequestTimeout:
		return "upload timed out"
	case http.StatusRequestEntityTooLarge:
		return "file exceeds the size limit (10 MB for text, Markdown, JSON and VTT, 30 MB for XML, 256 MB otherwise)"
	case http.StatusTooManyRequests:
		return "rate limit exceeded, retry later"
	case http.StatusServiceUnavailable:
		return "workspace storage capacity exceeded"
	}
	return ""
}

// userStatusHint covers the User Management API, whose keys must be created
// by a workspace admin.
func userStatusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid request body or role, or the change would leave the workspace without an active admin"
	case http.StatusUnauthorized:
		return "invalid, missing or expired API key, or the admin who created the key no longer exists"
	case http.StatusForbidden:
		return "API key lacks the USER_MANAGEMENT_API scope"
	case http.StatusNotFound:
		return "no active human workspace member with this email"
	}
	return ""
}

var errNoAPIKey = errors.New("LANGDOCK_API_KEY is not set; configure an API key with the INTEGRATION_API scope (integration tools), the Agent API scope (agent tools), the KNOWLEDGE_FOLDER_API scope (knowledge tools) and the USER_MANAGEMENT_API scope (user tools) in the MCP server's environment")

// doJSON sends body (if non-nil) as JSON and returns the raw response body.
func (c *Client) doJSON(ctx context.Context, method, path string, body any) ([]byte, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, method, path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(c.http, req, familyOf(path))
}

// formField is a plain-text multipart field sent before the file.
type formField struct{ name, value string }

// uploadFile sends data as a multipart/form-data file field, preceded by
// the given text fields.
func (c *Client) uploadFile(ctx context.Context, method, path, field, filename, contentType string, data []byte, fields ...formField) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := mw.WriteField(f.name, f.value); err != nil {
			return nil, err
		}
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
	h.Set("Content-Type", contentType)
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, method, path, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.send(c.upload, req, familyOf(path))
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if c.apiKey == "" {
		return nil, errNoAPIKey
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) send(hc *http.Client, req *http.Request, api apiFamily) ([]byte, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body)), API: api}
	}
	return body, nil
}
