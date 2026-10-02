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

Single package: `main.go` (wiring from env) → `client.go` (`Client`, a thin HTTP wrapper returning raw response bodies and `*APIError` for non-2xx) → `tools.go` (Integrations API: input structs, tool registration, handlers, shared schema helpers) `agents.go` (Agents API) and `knowledge.go` (Knowledge Folder API), the latter two registered from `register()`. The go-sdk owns the protocol, and tool input schemas are inferred from the input structs, then patched by `schemaFor` with enums and `maxLength` values that struct tags cannot express. Tool inputs deliberately use the API's camelCase field names so they match the Langdock docs one-to-one.

Handlers return Langdock's JSON response verbatim as text. A returned Go error becomes a tool-level error (`IsError`) the model can read. `APIError` adds a hint per documented status code and picks the hints per API family, which `familyOf` derives from the request path relative to the base URL (`/agent/v1/` for the Agents API, `/knowledge` for the Knowledge Folder API, everything else for the Integrations API).

## Invariants

- `update_action` must stay a read-merge-write. The API's PUT clears `description` and drops all input fields that are not resent, which would silently destroy an action when a caller only changes its code. `code` and `requiresConfirmation` are preserved by the API when omitted, so they are only sent when given. `jsonSchema` on input fields is not returned by `get_integration` but is preserved server-side when the field slug (derived from its label) is unchanged.
- `update_trigger` is a full replace and says so in its description. `get_integration` returns no `pollingCode` or trigger input fields, so a merge is impossible.
- `update_agent` must stay a plain pass-through PATCH, not a read-merge-write. The API already leaves omitted fields unchanged, and `get_agent` returns the published version, so merging from it would overwrite unpublished draft changes. `get_agent` also omits `slug`, `options`, `fileTypes` and `emailDomain` of input fields, so they cannot be round-tripped.
- `add_agent_actions` / `remove_agent_actions` are read-merge-writes over the published action list and say so in their descriptions. They keep action entries as raw JSON so undocumented properties such as `connectionId` survive; null values are dropped before resending.
- Multipart uploads (knowledge files and the integration icon) go through a separate `http.Client` with a 10-minute timeout, because knowledge files may be up to 256 MB. Their MIME type comes from the explicit extension map `knowledgeMIMETypes`, because content sniffing reports Office files as `application/zip`, which Langdock rejects.
- `export_usage` always calls the explicit `/json` or `/csv` route, never the format-less default, and both return a JSON envelope (rows, or a signed download URL). `group_by` is validated per `dataType` locally against `usageGroupBy`, because the schema enum cannot express the dependency. Its field name stays snake_case because the API uses `group_by`.
- `Client.send` fails with `errResponseTooLarge` instead of truncating bodies over 10 MB, because a cut-off body would reach the model as broken JSON.
- Usage Export API endpoints come from https://docs.langdock.com/en/developer/usage-export-api/intro-to-usage-export-api.md.
- Never log or echo the API key.
- `serverVersion` is a `var` because GoReleaser sets it from the tag via `-ldflags -X main.serverVersion=…`. Keep its default in step with the latest tag.
- Endpoint paths, methods and field limits come from https://docs.langdock.com/en/developer/integrations-api/, https://docs.langdock.com/en/developer/agents-api/ and https://docs.langdock.com/en/developer/knowledge-folder-api/ (index at https://docs.langdock.com/llms.txt). Re-check there before changing them.

## Gotchas

- MCP `2026-07-28` removed the `initialize` handshake in favour of the stateless `server/discover`. A hand-written stdio smoke test that sends `initialize` therefore always negotiates `2025-11-25`, which is correct SDK behaviour. `TestNegotiatesLatestProtocol` covers the new path with the SDK client.
- The stdio smoke test needs a trailing `sleep`, or the transport sees EOF and exits before responding:

```bash
go build -o langdock-mcp . && (printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'; sleep 1) | ./langdock-mcp
```
