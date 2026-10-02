# langdock-mcp

An MCP server that exposes Langdock's [Integrations API](https://docs.langdock.com/en/developer/integrations-api/integrations-overview) and the build endpoints of the [Agents API](https://docs.langdock.com/en/developer/agents-api/agents-overview) and the [Knowledge Folder API](https://docs.langdock.com/en/developer/knowledge-folder-api/knowledge-folder-overview) as tools. An MCP client such as Claude Code or Claude Desktop can use it to create and maintain custom Langdock integrations (actions, triggers, auth configuration, icon), agents, and the files and sharing of knowledge bases.

Langdock's own MCP server (`https://api.langdock.com/mcp`) only exposes workspace agents (`find_agent`, `ask_agent`). As of September 2026 there is no official MCP server for the Integrations API.

## Integration tools

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

## Agent tools

| Tool | Endpoint |
|---|---|
| `get_agent` | `GET /agent/v1/get?agentId=…` |
| `list_agent_models` | `GET /agent/v1/models` |
| `create_agent` | `POST /agent/v1/create` |
| `update_agent` | `PATCH /agent/v1/update` |
| `publish_agent` | `POST /agent/v1/publish` |
| `add_agent_actions` | `GET /agent/v1/get`, then `PATCH /agent/v1/update` with the merged action list |
| `remove_agent_actions` | `GET /agent/v1/get`, then `PATCH /agent/v1/update` with the remaining actions |

`create_agent` and `update_agent` only change the agent's draft. Users see the change once the draft is published, either with `publish_agent` or with `publish: true` (and an optional `publishDescription`) on the write itself. `publish_agent` returns 409 when the draft has no changes.

`update_agent` sends only the fields the caller passes, which the API applies as a partial update. Array fields (`conversationStarters`, `actions`, `inputFields`, `attachments`, `knowledgeFolderIds`) replace the whole list, and `[]` empties it. Empty strings clear `description` and `instruction`, and `clearEmoji` removes the emoji. The tool deliberately does not read before writing, because `get_agent` returns the published version and would overwrite unpublished draft changes.

`add_agent_actions` and `remove_agent_actions` change single actions without resending the list by hand. They read the current list via `get_agent` and therefore start from the published version. Action changes that exist only in the draft are lost. Existing action entries are resent unchanged, including properties the docs do not describe, such as a non-null `connectionId`; null-valued properties are dropped. `remove_agent_actions` writes nothing if one of the ids is not on the agent.

`create_agent` requires `model` (an `id` from `list_agent_models`), because a workspace may have no default model. The API cannot list agents or change who an agent is shared with; both are only possible in the Langdock UI.

## Knowledge tools

| Tool | Endpoint |
|---|---|
| `list_knowledge_bases` | `GET /knowledge` |
| `update_knowledge_base` | `PATCH /knowledge/{folderId}/folder` |
| `list_knowledge_files` | `GET /knowledge/{folderId}/list` |
| `get_knowledge_file` | `GET /knowledge/{folderId}/{attachmentId}` |
| `upload_knowledge_file` | `POST /knowledge/{folderId}` (multipart upload from a local file) |
| `replace_knowledge_file` | `PATCH /knowledge/{folderId}` (multipart upload from a local file) |
| `delete_knowledge_file` | `DELETE /knowledge/{folderId}/{attachmentId}` |
| `search_knowledge` | `POST /knowledge/search` |
| `grant_knowledge_access` | `POST /knowledge/{folderId}/access` |
| `update_knowledge_access` | `PATCH /knowledge/{folderId}/access` |
| `revoke_knowledge_access` | `DELETE /knowledge/{folderId}/access` |

The API cannot create or delete knowledge bases; both are only possible in the Langdock Library. A knowledge base is visible to these tools once it is shared with the API key, and every write (rename, file changes, sharing) needs the Editor role on it.

`list_knowledge_bases` only returns results for workspace API keys. Its ids are what `create_agent` and `update_agent` expect in `knowledgeFolderIds`.

`upload_knowledge_file` and `replace_knowledge_file` read a local file from an absolute path on the machine running the server. They enforce the documented size limits before sending (10 MB for text, Markdown, JSON and VTT, 30 MB for XML, 256 MB for other documents) and set the MIME type from the file extension. Langdock processes the file asynchronously after the upload returns, so `get_knowledge_file` has to be polled until `syncStatus` is `SYNCED` or a failure status.

`grant_knowledge_access` is all-or-nothing: if one target id is unknown or ineligible, nothing is granted. Groups can only be shared in the Langdock UI, and the owner's access cannot be changed or revoked.

## Usage export tools

| Tool | Endpoint |
|---|---|
| `export_usage` | `POST /export/{dataType}/json` or `/csv` (`dataType`: `users`, `agents`, `api-keys`, `projects`, `models`, `workflows`) |

`export_usage` needs an API key with the `USAGE_EXPORT_API` scope, which only workspace admins can grant and which exposes usage data of the whole workspace. `format: json` (the default) returns the rows inline; `format: csv` returns a signed download URL, which suits exports beyond the server's 10 MB response limit. `group_by` accepts `model` for users, agents and API keys, `source` or `deployment` (BYOK only) for models, and nothing for projects and workflows. One request scans at most 1,000,000 usage rows, so long periods have to be split.

## Configuration

| Variable | Required | Description |
|---|---|---|
| `LANGDOCK_API_KEY` | yes | API key created by a workspace admin in the Langdock workspace settings. Integration tools need the `INTEGRATION_API` scope, agent tools the Agent API scope and access to the agent, knowledge tools the `KNOWLEDGE_FOLDER_API` scope and access to the knowledge base. |
| `LANGDOCK_BASE_URL` | no | Defaults to `https://api.langdock.com`. Dedicated deployments use `https://<your-domain>/api/public`. |

The key is checked lazily. A missing key produces a tool error on the first call, not a startup failure. A 401 or 403 from an agent tool says that the key may lack the Agent API scope. A 403 from a knowledge tool says that the key may lack the `KNOWLEDGE_FOLDER_API` scope, the knowledge base may not be shared with it, or the write may need the Editor role.

## Installation

Homebrew (macOS and Linux):

```bash
brew install --cask dennisschroeder/tap/langdock-mcp
```

With Go:

```bash
go install github.com/dennisschroeder/langdock-mcp@latest
```

Claude Code:

```bash
claude mcp add langdock-integrations -s user -e LANGDOCK_API_KEY=<key> -- langdock-mcp
```

Claude Desktop (`~/Library/Application Support/Claude/claude_desktop_config.json`) does not inherit the shell `PATH`, so it needs the absolute path (`$(go env GOPATH)/bin/langdock-mcp` for a Go install):

```json
{
  "mcpServers": {
    "langdock-integrations": {
      "command": "/opt/homebrew/bin/langdock-mcp",
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

Pushing a `v*` tag runs GoReleaser, which publishes the release binaries and updates the cask in [dennisschroeder/homebrew-tap](https://github.com/dennisschroeder/homebrew-tap). The workflow needs a `HOMEBREW_TAP_GITHUB_TOKEN` secret with write access to the tap. The tag sets the version reported by `--version` and to MCP clients.

Tests run the real SDK client against the server over in-memory transports, with an `httptest` server standing in for Langdock.
