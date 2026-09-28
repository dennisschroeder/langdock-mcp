# langdock-mcp

An MCP server that exposes Langdock's [Integrations API](https://docs.langdock.com/en/developer/integrations-api/integrations-overview) as tools, so an MCP client such as Claude Code or Claude Desktop can create and maintain custom Langdock integrations, including their actions, triggers, auth configuration and icon.

Langdock's own MCP server (`https://api.langdock.com/mcp`) only exposes workspace agents (`find_agent`, `ask_agent`). As of September 2026 there is no official MCP server for the Integrations API.

## Tools

| Tool | Endpoint |
|---|---|
| `list_integrations` | `GET /integrations/v1/get` |
| `get_integration` | `GET /integrations/v1/{id}` |
| `create_integration` | `POST /integrations/v1/create` |
| `update_integration` | `PATCH /integrations/v1/{id}` |
| `update_integration_auth` | `PATCH /integrations/v1/{id}/auth` |
| `get_integration_icon` | `GET /integrations/v1/{id}/icon` |
| `replace_integration_icon` | `PUT /integrations/v1/{id}/icon` (multipart upload from a local file) |
| `create_action` | `POST /integrations/v1/{id}/actions/create` |
| `update_action` | `PUT /integrations/v1/{id}/actions/{actionId}`, with patch semantics (see below) |
| `delete_action` | `DELETE /integrations/v1/{id}/actions/{actionId}` |
| `create_trigger` | `POST /integrations/v1/{id}/triggers/create` |
| `update_trigger` | `PUT /integrations/v1/{id}/triggers/{triggerId}` (full replace) |
| `delete_trigger` | `DELETE /integrations/v1/{id}/triggers/{triggerId}` |

Tool input schemas carry the documented enums and length limits, so invalid input is rejected before it reaches Langdock.

`update_action` behaves like a patch, although the underlying endpoint is a PUT that clears `description` and removes every input field that is not resent. The tool first reads the current action via `get_integration` and resends everything the caller did not change. `clearDescription` and `clearInputFields` remove those values explicitly. `update_trigger` cannot do the same, because the API does not return a trigger's `pollingCode` or input fields.

The API offers no endpoint to delete an integration.

## Configuration

| Variable | Required | Description |
|---|---|---|
| `LANGDOCK_API_KEY` | yes | API key with the `INTEGRATION_API` scope, created by a workspace admin in the Langdock workspace settings |
| `LANGDOCK_BASE_URL` | no | Defaults to `https://api.langdock.com`. Dedicated deployments use `https://<your-domain>/api/public`. |

The key is checked lazily. A missing key produces a tool error on the first call, not a startup failure.

## Installation

```bash
go install github.com/dennisschroeder/langdock-mcp@latest
```

Claude Code:

```bash
claude mcp add langdock-integrations -s user -e LANGDOCK_API_KEY=<key> -- "$(go env GOPATH)/bin/langdock-mcp"
```

Claude Desktop (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "langdock-integrations": {
      "command": "/Users/<you>/go/bin/langdock-mcp",
      "env": { "LANGDOCK_API_KEY": "<key>" }
    }
  }
}
```

## Protocol

Built on `github.com/modelcontextprotocol/go-sdk` v1.8.0 over stdio. Clients that speak MCP `2026-07-28` reach it through the stateless `server/discover` call, and older clients that use the `initialize` handshake get `2025-11-25` or their own older version.

## Development

```bash
gofmt -l .
go vet ./...
go test ./...
```

Tests run the real SDK client against the server over in-memory transports, with an `httptest` server standing in for Langdock.
