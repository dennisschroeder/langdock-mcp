package main

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	knowledgeRoles       = []string{"USER", "EDITOR"}
	knowledgeTargetTypes = []string{"USER", "API_KEY"}
)

// knowledgeMIMETypes covers the documented file types, because
// mime.TypeByExtension depends on the host's mime.types and sniffing reports
// Office files as application/zip, which Langdock's validation rejects.
var knowledgeMIMETypes = map[string]string{
	".pdf":  "application/pdf",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".dotx": "application/vnd.openxmlformats-officedocument.wordprocessingml.template",
	".ppt":  "application/vnd.ms-powerpoint",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".potx": "application/vnd.openxmlformats-officedocument.presentationml.template",
	".msg":  "application/vnd.ms-outlook",
	".txt":  "text/plain",
	".md":   "text/markdown",
	".html": "text/html",
	".htm":  "text/html",
	".json": "application/json",
	".xml":  "application/xml",
	".vtt":  "text/vtt",
}

type ListKnowledgeBasesInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"page size, 1-100, default 50"`
	Cursor string `json:"cursor,omitempty" jsonschema:"nextCursor from the previous page"`
}

type KnowledgeBaseRef struct {
	FolderID string `json:"folderId" jsonschema:"ID of the knowledge base"`
}

type UpdateKnowledgeBaseInput struct {
	FolderID    string  `json:"folderId" jsonschema:"ID of the knowledge base"`
	Name        *string `json:"name,omitempty" jsonschema:"new name, must not be blank"`
	Description *string `json:"description,omitempty" jsonschema:"new description"`
}

type KnowledgeFileRef struct {
	FolderID     string `json:"folderId" jsonschema:"ID of the knowledge base"`
	AttachmentID string `json:"attachmentId" jsonschema:"ID of the file (attachment)"`
}

type UploadKnowledgeFileInput struct {
	FolderID string `json:"folderId" jsonschema:"ID of the knowledge base"`
	FilePath string `json:"filePath" jsonschema:"absolute path to a local document on the machine running this server"`
	URL      string `json:"url,omitempty" jsonschema:"source URL shown to users when the file is cited in an answer"`
}

type ReplaceKnowledgeFileInput struct {
	FolderID     string `json:"folderId" jsonschema:"ID of the knowledge base"`
	AttachmentID string `json:"attachmentId" jsonschema:"ID of the file (attachment) to replace"`
	FilePath     string `json:"filePath" jsonschema:"absolute path to the new local document on the machine running this server"`
	URL          string `json:"url,omitempty" jsonschema:"source URL shown to users when the file is cited in an answer"`
}

type SearchKnowledgeInput struct {
	Query string `json:"query" jsonschema:"natural-language search query"`
}

type GrantKnowledgeAccessInput struct {
	FolderID  string   `json:"folderId" jsonschema:"ID of the knowledge base"`
	TargetIDs []string `json:"targetIds" jsonschema:"user IDs and workspace API key IDs, 1-100; group IDs and personal API keys are not accepted"`
	Role      string   `json:"role" jsonschema:"USER (Viewer: search and retrieve) or EDITOR (upload, update, delete, share, rename)"`
}

type UpdateKnowledgeAccessInput struct {
	FolderID string `json:"folderId" jsonschema:"ID of the knowledge base"`
	Type     string `json:"type" jsonschema:"kind of target"`
	TargetID string `json:"targetId" jsonschema:"user ID or workspace API key ID"`
	Role     string `json:"role" jsonschema:"USER (Viewer) or EDITOR"`
}

type RevokeKnowledgeAccessInput struct {
	FolderID string `json:"folderId" jsonschema:"ID of the knowledge base"`
	Type     string `json:"type" jsonschema:"kind of target"`
	TargetID string `json:"targetId" jsonschema:"user ID or workspace API key ID"`
}

const (
	noCreateNote   = " The API cannot create or delete knowledge bases; that is only possible in the Langdock Library."
	processingNote = " Processing (extraction, embedding) continues asynchronously; poll get_knowledge_file until syncStatus is SYNCED or a *_FAILED / TIMEOUT status."
	fileLimitsNote = " Size limits: 10 MB for text, Markdown, JSON and VTT, 30 MB for XML, 256 MB for other documents. Spreadsheets, images, audio and video are rejected."
)

// knowledgeIDs requires non-empty ids, because an empty one silently
// addresses a different route, such as DELETE /knowledge/{folderId}/.
func knowledgeIDs(sc *jsonschema.Schema) {
	for _, p := range []string{"folderId", "attachmentId"} {
		if prop, ok := sc.Properties[p]; ok {
			prop.MinLength = ptr(1)
		}
	}
}

func (s *Server) registerKnowledge() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)}
	additive := &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)}
	destructive := &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(true)}

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_knowledge_bases",
		Description: "List the knowledge bases the API key can use, with id, name, description, file count and the key's role (USER = Viewer, EDITOR). Newest first; pass nextCursor as cursor for the next page. Only workspace API keys see knowledge bases. Pass an id as knowledgeFolderIds to create_agent / update_agent." + noCreateNote,
		Annotations: readOnly,
		InputSchema: schemaFor[ListKnowledgeBasesInput](func(sc *jsonschema.Schema) {
			at(sc, "limit").Minimum = ptr(1.0)
			at(sc, "limit").Maximum = ptr(100.0)
		}),
	}, s.listKnowledgeBases)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_knowledge_base",
		Description: "Rename a knowledge base or change its description. Only the fields you pass change. Needs the Editor role." + noCreateNote,
		Annotations: destructive,
		InputSchema: schemaFor[UpdateKnowledgeBaseInput](func(sc *jsonschema.Schema) {
			knowledgeIDs(sc)
			at(sc, "name").MinLength = ptr(1)
		}),
	}, s.updateKnowledgeBase)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_knowledge_files",
		Description: "List the files in a knowledge base with their processing status (syncStatus), page count and summary.",
		Annotations: readOnly,
		InputSchema: schemaFor[KnowledgeBaseRef](knowledgeIDs),
	}, s.listKnowledgeFiles)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "get_knowledge_file",
		Description: "Get one file of a knowledge base, including syncStatus and, if processing failed, syncMessage.",
		Annotations: readOnly,
		InputSchema: schemaFor[KnowledgeFileRef](knowledgeIDs),
	}, s.getKnowledgeFile)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "upload_knowledge_file",
		Description: "Upload a local document (PDF, Word, PowerPoint, text, Markdown, HTML and others) to a knowledge base as a new file. Needs the Editor role." + fileLimitsNote + processingNote,
		Annotations: additive,
		InputSchema: schemaFor[UploadKnowledgeFileInput](knowledgeIDs),
	}, s.uploadKnowledgeFile)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "replace_knowledge_file",
		Description: "Replace an existing knowledge base file with a new local document. The old version and its embeddings are removed. Needs the Editor role." + fileLimitsNote + processingNote,
		Annotations: destructive,
		InputSchema: schemaFor[ReplaceKnowledgeFileInput](knowledgeIDs),
	}, s.replaceKnowledgeFile)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "delete_knowledge_file",
		Description: "Permanently delete a file and its embeddings from a knowledge base. Cannot be undone. Needs the Editor role.",
		Annotations: destructive,
		InputSchema: schemaFor[KnowledgeFileRef](knowledgeIDs),
	}, s.deleteKnowledgeFile)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "search_knowledge",
		Description: "Semantic search across all knowledge bases shared with the API key. Returns the best-matching chunk per document with similarity (0-1), attachment id (subsource), filename (subname) and source url.",
		Annotations: readOnly,
		InputSchema: schemaFor[SearchKnowledgeInput](func(sc *jsonschema.Schema) {
			at(sc, "query").MinLength = ptr(1)
		}),
	}, s.searchKnowledge)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "grant_knowledge_access",
		Description: "Grant people or workspace API keys a role on a knowledge base. All-or-nothing: if any id is unknown or ineligible, nothing is granted. Groups can only be shared in the Langdock UI. Needs the Editor role.",
		Annotations: additive,
		InputSchema: schemaFor[GrantKnowledgeAccessInput](func(sc *jsonschema.Schema) {
			knowledgeIDs(sc)
			at(sc, "targetIds").MinItems = ptr(1)
			at(sc, "targetIds").MaxItems = ptr(100)
			enum(sc, knowledgeRoles, "role")
		}),
	}, s.grantKnowledgeAccess)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_knowledge_access",
		Description: "Change the role of a person or workspace API key on a knowledge base; creates the grant if none exists. The owner's access cannot be changed. Needs the Editor role.",
		Annotations: destructive,
		InputSchema: schemaFor[UpdateKnowledgeAccessInput](func(sc *jsonschema.Schema) {
			knowledgeIDs(sc)
			enum(sc, knowledgeTargetTypes, "type")
			enum(sc, knowledgeRoles, "role")
		}),
	}, s.updateKnowledgeAccess)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "revoke_knowledge_access",
		Description: "Revoke a person's or workspace API key's access to a knowledge base. The owner's access cannot be revoked. Revoking the API key's own access locks this server out of the knowledge base. Needs the Editor role.",
		Annotations: destructive,
		InputSchema: schemaFor[RevokeKnowledgeAccessInput](func(sc *jsonschema.Schema) {
			knowledgeIDs(sc)
			enum(sc, knowledgeTargetTypes, "type")
		}),
	}, s.revokeKnowledgeAccess)
}

func (s *Server) listKnowledgeBases(ctx context.Context, _ *mcp.CallToolRequest, in ListKnowledgeBasesInput) (*mcp.CallToolResult, any, error) {
	q := url.Values{}
	if in.Limit != 0 {
		q.Set("limit", strconv.Itoa(in.Limit))
	}
	if in.Cursor != "" {
		q.Set("cursor", in.Cursor)
	}
	path := "/knowledge"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return s.call(ctx, http.MethodGet, path, nil)
}

func (s *Server) updateKnowledgeBase(ctx context.Context, _ *mcp.CallToolRequest, in UpdateKnowledgeBaseInput) (*mcp.CallToolResult, any, error) {
	if in.Name == nil && in.Description == nil {
		return nil, nil, fmt.Errorf("nothing to update: pass name or description")
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return nil, nil, fmt.Errorf("name must not be blank")
	}
	body := struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
	}{in.Name, in.Description}
	return s.call(ctx, http.MethodPatch, knowledgePath(in.FolderID)+"/folder", body)
}

func (s *Server) listKnowledgeFiles(ctx context.Context, _ *mcp.CallToolRequest, in KnowledgeBaseRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodGet, knowledgePath(in.FolderID)+"/list", nil)
}

func (s *Server) getKnowledgeFile(ctx context.Context, _ *mcp.CallToolRequest, in KnowledgeFileRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodGet, knowledgeFilePath(in.FolderID, in.AttachmentID), nil)
}

func (s *Server) uploadKnowledgeFile(ctx context.Context, _ *mcp.CallToolRequest, in UploadKnowledgeFileInput) (*mcp.CallToolResult, any, error) {
	var fields []formField
	if in.URL != "" {
		fields = append(fields, formField{"url", in.URL})
	}
	return s.sendKnowledgeFile(ctx, http.MethodPost, in.FolderID, in.FilePath, fields)
}

func (s *Server) replaceKnowledgeFile(ctx context.Context, _ *mcp.CallToolRequest, in ReplaceKnowledgeFileInput) (*mcp.CallToolResult, any, error) {
	fields := []formField{{"attachmentId", in.AttachmentID}}
	if in.URL != "" {
		fields = append(fields, formField{"url", in.URL})
	}
	return s.sendKnowledgeFile(ctx, http.MethodPatch, in.FolderID, in.FilePath, fields)
}

func (s *Server) sendKnowledgeFile(ctx context.Context, method, folderID, filePath string, fields []formField) (*mcp.CallToolResult, any, error) {
	if !filepath.IsAbs(filePath) {
		return nil, nil, fmt.Errorf("filePath must be absolute, got %q", filePath)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s is not a regular file", filePath)
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	if limit := knowledgeFileLimit(ext); info.Size() > limit {
		return nil, nil, fmt.Errorf("%s is %d bytes, the limit for %s files is %d MB", filePath, info.Size(), ext, limit>>20)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, err
	}
	body, err := s.client.uploadFile(ctx, method, knowledgePath(folderID), "file", filepath.Base(filePath), knowledgeContentType(ext, data), data, fields...)
	if err != nil {
		return nil, nil, err
	}
	return textResult(body), nil, nil
}

// knowledgeFileLimit mirrors the documented per-type upload limits so an
// oversized file fails before its bytes are sent.
func knowledgeFileLimit(ext string) int64 {
	switch ext {
	case ".txt", ".md", ".json", ".vtt":
		return 10 << 20
	case ".xml":
		return 30 << 20
	}
	return 256 << 20
}

func knowledgeContentType(ext string, data []byte) string {
	if t, ok := knowledgeMIMETypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return http.DetectContentType(data)
}

func (s *Server) deleteKnowledgeFile(ctx context.Context, _ *mcp.CallToolRequest, in KnowledgeFileRef) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodDelete, knowledgeFilePath(in.FolderID, in.AttachmentID), nil)
}

func (s *Server) searchKnowledge(ctx context.Context, _ *mcp.CallToolRequest, in SearchKnowledgeInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, "/knowledge/search", in)
}

func (s *Server) grantKnowledgeAccess(ctx context.Context, _ *mcp.CallToolRequest, in GrantKnowledgeAccessInput) (*mcp.CallToolResult, any, error) {
	body := struct {
		TargetIDs []string `json:"targetIds"`
		Role      string   `json:"role"`
	}{in.TargetIDs, in.Role}
	return s.call(ctx, http.MethodPost, knowledgePath(in.FolderID)+"/access", body)
}

func (s *Server) updateKnowledgeAccess(ctx context.Context, _ *mcp.CallToolRequest, in UpdateKnowledgeAccessInput) (*mcp.CallToolResult, any, error) {
	body := struct {
		Type     string `json:"type"`
		TargetID string `json:"targetId"`
		Role     string `json:"role"`
	}{in.Type, in.TargetID, in.Role}
	return s.call(ctx, http.MethodPatch, knowledgePath(in.FolderID)+"/access", body)
}

func (s *Server) revokeKnowledgeAccess(ctx context.Context, _ *mcp.CallToolRequest, in RevokeKnowledgeAccessInput) (*mcp.CallToolResult, any, error) {
	body := struct {
		Type     string `json:"type"`
		TargetID string `json:"targetId"`
	}{in.Type, in.TargetID}
	return s.call(ctx, http.MethodDelete, knowledgePath(in.FolderID)+"/access", body)
}

func knowledgePath(folderID string) string {
	return "/knowledge/" + url.PathEscape(folderID)
}

func knowledgeFilePath(folderID, attachmentID string) string {
	return knowledgePath(folderID) + "/" + url.PathEscape(attachmentID)
}
