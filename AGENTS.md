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

Single package, three files: `main.go` (wiring from env) → `client.go` (`Client`, a thin HTTP wrapper returning raw response bodies and `*APIError` for non-2xx) → `tools.go` (input structs, tool registration, handlers). The go-sdk owns the protocol, and tool input schemas are inferred from the input structs, then patched by `schemaFor` with enums and `maxLength` values that struct tags cannot express. Tool inputs deliberately use the API's camelCase field names so they match the Langdock docs one-to-one.

Handlers return Langdock's JSON response verbatim as text. A returned Go error becomes a tool-level error (`IsError`) the model can read. `APIError` adds a hint per documented status code.

## Invariants

- `update_action` must stay a read-merge-write. The API's PUT clears `description` and drops all input fields that are not resent, which would silently destroy an action when a caller only changes its code. `code` and `requiresConfirmation` are preserved by the API when omitted, so they are only sent when given. `jsonSchema` on input fields is not returned by `get_integration` but is preserved server-side when the field slug (derived from its label) is unchanged.
- `update_trigger` is a full replace and says so in its description. `get_integration` returns no `pollingCode` or trigger input fields, so a merge is impossible.
- Never log or echo the API key.
- Endpoint paths, methods and field limits come from https://docs.langdock.com/en/developer/integrations-api/ (index at https://docs.langdock.com/llms.txt). Re-check there before changing them.

## Gotchas

- MCP `2026-07-28` removed the `initialize` handshake in favour of the stateless `server/discover`. A hand-written stdio smoke test that sends `initialize` therefore always negotiates `2025-11-25`, which is correct SDK behaviour. `TestNegotiatesLatestProtocol` covers the new path with the SDK client.
- The stdio smoke test needs a trailing `sleep`, or the transport sees EOF and exits before responding:

```bash
(printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'; sleep 1) | go run .
```
