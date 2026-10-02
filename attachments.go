package main

import (
	"context"
	"net/http"
	"path/filepath"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type UploadAttachmentInput struct {
	FilePath string `json:"filePath" jsonschema:"absolute path to a local file on the machine running this server"`
}

type AttachmentRef struct {
	AttachmentID string `json:"attachmentId" jsonschema:"UUID of the attachment"`
}

func (s *Server) registerAttachments() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "upload_attachment",
		Description: "Upload a local file as a Langdock attachment and return its attachmentId. Pass the id in a message's metadata.attachments for chat_with_agent, or in an agent's attachments via update_agent. Executables and some other file types are rejected; the size limit depends on the type (up to 256 MB). Needs the KNOWLEDGE_FOLDER_API scope.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)},
	}, s.uploadAttachment)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "delete_attachment",
		Description: "Delete an attachment. It can no longer be referenced in agent conversations and cannot be restored through the API.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)},
		InputSchema: schemaFor[AttachmentRef](func(sc *jsonschema.Schema) {
			at(sc, "attachmentId").Pattern = uuidPattern
		}),
	}, s.deleteAttachment)
}

func (s *Server) uploadAttachment(ctx context.Context, _ *mcp.CallToolRequest, in UploadAttachmentInput) (*mcp.CallToolResult, any, error) {
	data, ext, err := readUploadFile(in.FilePath, func(string) int64 { return 256 << 20 })
	if err != nil {
		return nil, nil, err
	}
	body, err := s.client.uploadFile(ctx, http.MethodPost, "/attachment/v1/upload", "file", filepath.Base(in.FilePath), knowledgeContentType(ext, data), data)
	if err != nil {
		return nil, nil, err
	}
	return textResult(body), nil, nil
}

func (s *Server) deleteAttachment(ctx context.Context, _ *mcp.CallToolRequest, in AttachmentRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodDelete, "/attachment/v1/delete", in)
}

// attachmentStatusHint covers the attachment endpoints, which use the
// Knowledge Folder API scope although they belong to the Agents API docs.
func attachmentStatusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid request, or the file failed validation"
	case http.StatusUnauthorized:
		return "invalid or missing API key"
	case http.StatusForbidden:
		return "API key lacks the KNOWLEDGE_FOLDER_API scope or has no access to this attachment"
	case http.StatusNotFound:
		return "attachment not found"
	case http.StatusNotAcceptable:
		return "file type is blocked"
	case http.StatusRequestEntityTooLarge:
		return "file exceeds the size limit for its type"
	case http.StatusTooManyRequests:
		return "rate limit exceeded, retry later"
	}
	return ""
}
