# AGENTS.md

Guidance for coding agents working in this repository.

## Commands

```bash
go build ./...
go vet ./...
go test ./...                       # all tests
go test -run TestUpdateAction ./...  # single test
gofmt -l .                          # must print nothing
```

## Architecture

Single package: `main.go` (wiring from env) → `client.go` (`Client`, a thin HTTP wrapper returning raw response bodies and `*APIError` for non-2xx) → `tools.go` (Integrations API: input structs, tool registration, handlers, shared schema helpers). Every other API lives in its own file, registered from `register()`:

- `agents.go`: Agents API
- `knowledge.go`: Knowledge Folder API
- `prompts.go`: Prompt Library API
- `usage.go`: Usage Export API
- `users.go`: User Management API
- `workflows.go`: Workflow API and Workflow Run Export API

The go-sdk owns the protocol, and tool input schemas are inferred from the input structs, then patched by `schemaFor` with enums and `maxLength` values that struct tags cannot express. Tool inputs deliberately use the API's camelCase field names so they match the Langdock docs one-to-one.

Handlers return Langdock's JSON response verbatim as text. A returned Go error becomes a tool-level error (`IsError`) the model can read. `APIError` adds a hint per documented status code and picks the hints per API family, which `familyOf` derives from the request path prefix relative to the base URL. Paths without a known prefix belong to the Integrations API. Newer API files keep their own status-hint function, and shared lists (the files above, the scope table in the README, `errNoAPIKey`) hold one entry per line, so parallel API branches merge by keeping both sides.

## Invariants

- `update_action` must stay a read-merge-write. The API's PUT clears `description` and drops all input fields that are not resent, which would silently destroy an action when a caller only changes its code. `code` and `requiresConfirmation` are preserved by the API when omitted, so they are only sent when given. `jsonSchema` on input fields is not returned by `get_integration` but is preserved server-side when the field slug (derived from its label) is unchanged.
- `update_trigger` is a full replace and says so in its description. `get_integration` returns no `pollingCode` or trigger input fields, so a merge is impossible.
- `update_agent` must stay a plain pass-through PATCH, not a read-merge-write. The API already leaves omitted fields unchanged, and `get_agent` returns the published version, so merging from it would overwrite unpublished draft changes. `get_agent` also omits `slug`, `options`, `fileTypes` and `emailDomain` of input fields, so they cannot be round-tripped.
- `add_agent_actions` / `remove_agent_actions` are read-merge-writes over the published action list and say so in their descriptions. They keep action entries as raw JSON so undocumented properties such as `connectionId` survive; null values are dropped before resending.
- `update_prompt` and `update_prompt_folder` must stay plain pass-through PATCHes. The API leaves omitted fields unchanged, so a read-merge-write would add nothing. `promptFolderId` and `sharedWithGroupId` are cleared with an explicit null, sent via the `clearPromptFolderId` / `clearSharedWithGroupId` flags.
- Prompt and prompt folder path ids are validated as UUIDs, because a non-UUID id such as `folders` addresses a different route under `/prompts/v1`.
- `update_workflow` must stay a plain pass-through PATCH. The docs state that omitted `limits` fields stay unchanged and treat the endpoint as a partial update (a body with no fields is a 400), so omitted metadata is assumed unchanged too; graph writes only touch the draft graph (version `0`), never `activeVersion`. `nodes` and `edges` replace the whole draft, so both are required together. The API rejects metadata and graph changes in one request, and the tool rejects them locally rather than splitting them into two non-atomic writes. Removing a limit needs an explicit `null`, which `removeLimits` sends.
- `delete_workflow` is permanent and has no confirmation parameter; its description must keep saying so.
- Multipart uploads (knowledge files and the integration icon) go through a separate `http.Client` with a 10-minute timeout, because knowledge files may be up to 256 MB. Their MIME type comes from the explicit extension map `knowledgeMIMETypes`, because content sniffing reports Office files as `application/zip`, which Langdock rejects.
- `export_usage` always calls the explicit `/json` or `/csv` route, never the format-less default, and both return a JSON envelope (rows, or a signed download URL). `group_by` is validated per `dataType` locally against `usageGroupBy`, because the schema enum cannot express the dependency. Its field name stays snake_case because the API uses `group_by`.
- `Client.send` fails with `errResponseTooLarge` instead of truncating bodies over 10 MB, because a cut-off body would reach the model as broken JSON.
- Usage Export API endpoints come from https://docs.langdock.com/en/developer/usage-export-api/intro-to-usage-export-api.md.
- `invite_users` sends emails and `update_user_role` / `deactivate_user` change access, so their descriptions must keep stating those side effects. The role enum is lowercase because the API rejects other casing.
- Never log or echo the API key.
- `serverVersion` is a `var` because GoReleaser sets it from the tag via `-ldflags -X main.serverVersion=…`. Keep its default in step with the latest tag.
- Endpoint paths, methods and field limits come from the Langdock docs (index at https://docs.langdock.com/llms.txt). Re-check there before changing them:
  - Integrations API: https://docs.langdock.com/en/developer/integrations-api/
  - Agents API: https://docs.langdock.com/en/developer/agents-api/
  - Knowledge Folder API: https://docs.langdock.com/en/developer/knowledge-folder-api/
  - Prompt Library API: https://docs.langdock.com/en/developer/prompts-api/
  - User Management API: https://docs.langdock.com/en/developer/user-management-api/
- Workflow API endpoints come from https://docs.langdock.com/en/developer/workflow-api/workflows-overview.md and the Workflow Run Export API from https://docs.langdock.com/en/developer/workflow-api/intro-to-workflow-api.md. `familyOf` maps `/workflows/v1/` to the Workflow API hints and other `/workflows/` paths to the export hints.

## Gotchas

- MCP `2026-07-28` removed the `initialize` handshake in favour of the stateless `server/discover`. A hand-written stdio smoke test that sends `initialize` therefore always negotiates `2025-11-25`, which is correct SDK behaviour. `TestNegotiatesLatestProtocol` covers the new path with the SDK client.
- The stdio smoke test needs a trailing `sleep`, or the transport sees EOF and exits before responding:

```bash
go build -o langdock-mcp . && (printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'; sleep 1) | ./langdock-mcp
```
