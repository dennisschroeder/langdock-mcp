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

// Client is a thin wrapper around Langdock's Integrations and Agents APIs. It returns
// response bodies verbatim so tools can hand them to the model unchanged.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// APIError is a non-2xx response from Langdock.
type APIError struct {
	Status int
	Body   string
	// Agent marks a response from the Agents API, whose status codes mean
	// something different than the Integrations API's.
	Agent bool
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("Langdock API returned %d", e.Status)
	hint := statusHint(e.Status)
	if e.Agent {
		hint = agentStatusHint(e.Status)
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

var errNoAPIKey = errors.New("LANGDOCK_API_KEY is not set; configure an API key with the INTEGRATION_API scope (integration tools) and the Agent API scope (agent tools) in the MCP server's environment")

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
	return c.send(req)
}

// uploadFile sends data as a single multipart/form-data file field.
func (c *Client) uploadFile(ctx context.Context, method, path, field, filename, contentType string, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
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
	return c.send(req)
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

func (c *Client) send(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body)), Agent: strings.Contains(req.URL.Path, "/agent/v1/")}
	}
	return body, nil
}
