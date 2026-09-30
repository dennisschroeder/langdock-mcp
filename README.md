# langdock-mcp

[![CI](https://github.com/dennisschroeder/langdock-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/dennisschroeder/langdock-mcp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/dennisschroeder/langdock-mcp)](https://github.com/dennisschroeder/langdock-mcp/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/dennisschroeder/langdock-mcp.svg)](https://pkg.go.dev/github.com/dennisschroeder/langdock-mcp)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

An [MCP](https://modelcontextprotocol.io) server for Langdock's [Integrations API](https://docs.langdock.com/en/developer/integrations-api/integrations-overview). It lets an MCP client such as Claude Code or Claude Desktop create and maintain custom Langdock integrations, including their actions, triggers, auth configuration and icon.

Langdock's own MCP server (`https://api.langdock.com/mcp`) only exposes workspace agents (`find_agent`, `ask_agent`). As of September 2026 there is no official MCP server for the Integrations API.

> This is an independent community project and is not affiliated with or endorsed by Langdock.

## Quick start

1. Have a workspace admin create an API key with the `INTEGRATION_API` scope in the Langdock workspace settings.
2. [Install the binary](#installation).
3. Register it with your MCP client and pass the key as `LANGDOCK_API_KEY`.

Then ask your client things like:

- "List my Langdock integrations."
- "Add an action `get_ticket` to the Jira integration that fetches a ticket by key."
- "Change only the code of action X, keep its input fields."

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

The key can change every integration in the workspace. Store it only in your MCP client configuration, never in a repository. The server does not log it.

## Installation

### Prebuilt binary

Download the archive for your platform from the [latest release](https://github.com/dennisschroeder/langdock-mcp/releases/latest) (macOS, Linux and Windows, each for amd64 and arm64), verify it against `checksums.txt`, and put `langdock-mcp` on your `PATH`.

On macOS, a binary downloaded with a browser is quarantined by Gatekeeper. Clear the flag once:

```bash
xattr -d com.apple.quarantine /path/to/langdock-mcp
```

### With Go

Requires the Go version from `go.mod`.

```bash
go install github.com/dennisschroeder/langdock-mcp@latest
```

The binary lands in `$(go env GOPATH)/bin`.

## Client setup

Claude Code:

```bash
claude mcp add langdock-integrations -s user -e LANGDOCK_API_KEY=<key> -- /path/to/langdock-mcp
```

Claude Desktop (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "langdock-integrations": {
      "command": "/path/to/langdock-mcp",
      "env": { "LANGDOCK_API_KEY": "<key>" }
    }
  }
}
```

## Protocol

Built on `github.com/modelcontextprotocol/go-sdk` v1.8.0 over stdio. Clients that speak MCP `2026-07-28` reach it through the stateless `server/discover` call, and older clients that use the `initialize` handshake get `2025-11-25` or their own older version.

## Contributing

Issues and pull requests are welcome. Please run the checks below before opening a PR.

## Development

```bash
gofmt -l .
go vet ./...
go test ./...
```

Tests run the real SDK client against the server over in-memory transports, with an `httptest` server standing in for Langdock.

## Releasing

Push a `v*` tag. CI runs the tests, then GoReleaser builds the binaries and publishes a GitHub release.

```bash
git tag v0.1.0 && git push origin v0.1.0
```

## License

[MIT](LICENSE)
