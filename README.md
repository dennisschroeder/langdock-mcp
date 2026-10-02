# langdock-mcp

An MCP server that exposes these Langdock APIs as tools, so that an MCP client such as Claude Code or Claude Desktop can build and administer a Langdock workspace:

- [Integrations API](https://docs.langdock.com/en/developer/integrations-api/integrations-overview): custom integrations with their actions, triggers, auth configuration and icon
- [Agents API](https://docs.langdock.com/en/developer/agents-api/agents-overview) (build endpoints): agents
- [Knowledge Folder API](https://docs.langdock.com/en/developer/knowledge-folder-api/knowledge-folder-overview): files and sharing of knowledge bases
- [Prompt Library API](https://docs.langdock.com/en/developer/prompts-api/prompts-overview): prompts and prompt folders
- [Scheduled Tasks API](https://docs.langdock.com/en/developer/scheduled-tasks-api/scheduled-tasks-overview): scheduled tasks of a service account
- [Usage Export API](https://docs.langdock.com/en/developer/usage-export-api/intro-to-usage-export-api): workspace usage data
- [User Management API](https://docs.langdock.com/en/developer/user-management-api/user-management-overview): workspace membership
- [Workflow API](https://docs.langdock.com/en/developer/workflow-api/workflows-overview): workflows and their run history

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

## Prompt tools

| Tool | Endpoint |
|---|---|
| `list_prompts` | `GET /prompts/v1` |
| `create_prompt` | `POST /prompts/v1` |
| `get_prompt` | `GET /prompts/v1/{promptId}` |
| `update_prompt` | `PATCH /prompts/v1/{promptId}` |
| `delete_prompt` | `DELETE /prompts/v1/{promptId}` |
| `list_prompt_folders` | `GET /prompts/v1/folders` |
| `create_prompt_folder` | `POST /prompts/v1/folders` |
| `get_prompt_folder` | `GET /prompts/v1/folders/{folderId}` |
| `update_prompt_folder` | `PATCH /prompts/v1/folders/{folderId}` |
| `delete_prompt_folder` | `DELETE /prompts/v1/folders/{folderId}` |

The Prompt Library API acts with the permissions of the API key's owner. `update_prompt` and `update_prompt_folder` send only the fields the caller passes, which the API applies as a partial update. `clearPromptFolderId` takes a prompt out of its folder and `clearSharedWithGroupId` stops sharing a folder with its group. A prompt cannot be in a folder and shared with the workspace at the same time, and a folder cannot be shared with the workspace and a group at the same time. `delete_prompt_folder` also deletes every prompt in the folder.

## Usage export tools

| Tool | Endpoint |
|---|---|
| `export_usage` | `POST /export/{dataType}/json` or `/csv` (`dataType`: `users`, `agents`, `api-keys`, `projects`, `models`, `workflows`) |

`export_usage` needs an API key with the `USAGE_EXPORT_API` scope, which only workspace admins can grant and which exposes usage data of the whole workspace. `format: json` (the default) returns the rows inline; `format: csv` returns a signed download URL, which suits exports beyond the server's 10 MB response limit. `group_by` accepts `model` for users, agents and API keys, `source` or `deployment` (BYOK only) for models, and nothing for projects and workflows. One request scans at most 1,000,000 usage rows, so long periods have to be split.

## User management tools

| Tool | Endpoint |
|---|---|
| `invite_users` | `POST /user-management/v1/invite` |
| `update_user_role` | `POST /user-management/v1/update-user-role` |
| `deactivate_user` | `POST /user-management/v1/deactivate-user` |

`invite_users` sends an invitation email to every new address and approves pending join requests from them. Existing members are skipped, and a 200 response can still list rejected addresses in `invalidEmails`. Roles are `member`, `editor` and `admin`; the API refuses to demote the last active admin. `deactivate_user` revokes access immediately but keeps the user's data for a later re-invite; the API cannot reactivate users.

## Workflow tools

| Tool | Endpoint |
|---|---|
| `list_workflows` | `GET /workflows/v1/list` |
| `get_workflow` | `GET /workflows/v1/get?workflowId=…` |
| `create_workflow` | `POST /workflows/v1/create` |
| `update_workflow` | `PATCH /workflows/v1/update` |
| `publish_workflow` | `POST /workflows/v1/publish` |
| `delete_workflow` | `DELETE /workflows/v1/delete?workflowId=…` (permanent) |
| `list_workflow_runs` | `GET /workflows/v1/runs?workflowId=…` (paginated) |
| `export_workflow_runs` | `GET /workflows/{workflowId}/runs?from=…&to=…` (Workflow Run Export API) |

The key needs the `WORKFLOW_API` scope to read, list runs and export, `WORKFLOW_WRITE_API` to create, update and publish, and `WORKFLOW_DELETE_API` to delete. Only workspace keys can hold the delete scope.

A workflow has a draft graph (`nodes` and `edges`, version `0`) and, once published, a separate published graph (`activeVersion`). `create_workflow` creates an inactive draft. `update_workflow` only ever changes the draft graph, so the published version keeps running until `publish_workflow` makes the draft the new active version and starts its schedules and webhooks. `update_workflow` sends only the fields the caller passes: omitted metadata and omitted `limits` fields stay unchanged (the docs state this explicitly only for `limits`), `removeLimits` sets caps to null, and `nodes` with `edges` replaces the whole draft graph. The API rejects metadata and graph changes in one request, so the tool refuses such calls before sending. Redacted secrets from `get_workflow` can be sent back unchanged and keep their stored values.

`delete_workflow` permanently deletes the workflow with all versions and asks for no confirmation. Use `update_workflow` with `status: "INACTIVE"` to pause a workflow instead.

`list_workflow_runs` pages with a cursor and filters by run, mode, status, version and date range. `export_workflow_runs` returns flat rows per node execution for a required date range, is not paginated and fails above 10,000 runs or 8,000,000 payload bytes.

## Scheduled task tools

| Tool | Endpoint |
|---|---|
| `list_scheduled_tasks` | `GET /automations/v1` |
| `create_scheduled_task` | `POST /automations/v1` |
| `get_scheduled_task` | `GET /automations/v1/{automationId}` |
| `update_scheduled_task` | `PATCH /automations/v1/{automationId}` |
| `delete_scheduled_task` | `DELETE /automations/v1/{automationId}` |
| `pause_scheduled_task` | `POST /automations/v1/{automationId}/pause` |
| `resume_scheduled_task` | `POST /automations/v1/{automationId}/resume` |
| `run_scheduled_task` | `POST /automations/v1/{automationId}/run` |

These tools need a workspace API key of a service account with the `AUTOMATION_API` scope (labelled Scheduled Tasks API), and Scheduled Tasks must be enabled for that account. Personal API keys are rejected. The key only sees and changes the tasks it created, at most 10 per owner.

`update_scheduled_task` sends only the fields the caller passes, and the API keeps everything else. The schedule is validated after merging with the stored clock fields. Passed arrays replace the stored list. `clearAssistantId` and `clearTaggedAssistantId` remove the agents. `run_scheduled_task` only enqueues a run and returns its `runId`; it also works on paused and `MANUAL` tasks and does not resume them.

## Configuration

| Variable | Required | Description |
|---|---|---|
| `LANGDOCK_API_KEY` | yes | API key created by a workspace admin in the Langdock workspace settings, with the scopes from the table below. |
| `LANGDOCK_BASE_URL` | no | Defaults to `https://api.langdock.com`. Dedicated deployments use `https://<your-domain>/api/public`. |

Each tool family needs its own scope on the key:

| Tools | Scope |
|---|---|
| Integration tools | `INTEGRATION_API` |
| Agent tools | Agent API scope, plus access to the agent |
| Knowledge tools | `KNOWLEDGE_FOLDER_API`, plus access to the knowledge base |
| Prompt tools | `PROMPT_API` |
| Scheduled task tools | `AUTOMATION_API` (labelled Scheduled Tasks API), on a service-account workspace key |
| `export_usage` | `USAGE_EXPORT_API` |
| User management tools | `USER_MANAGEMENT_API` |
| Workflow tools | `WORKFLOW_API` to read, list runs and export, `WORKFLOW_WRITE_API` to create, update and publish, `WORKFLOW_DELETE_API` to delete, plus access to the workflow |

The key is checked lazily.
A missing key produces a tool error on the first call, not a startup failure.
A 401 or 403 from an agent tool says that the key may lack the Agent API scope.
A 403 from a knowledge tool says that the key may lack the `KNOWLEDGE_FOLDER_API` scope, the knowledge base may not be shared with it, or the write may need the Editor role.
A 403 from a user management tool says that the key may lack the `USER_MANAGEMENT_API` scope.
A 403 from a workflow tool names the workflow scope the call needs, and for update, publish and run listing it can also mean that the workflow does not exist.
A 403 from a prompt tool says that the key may lack the `PROMPT_API` scope, or that its owner lacks write access or the `sharePrompts` permission.

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
