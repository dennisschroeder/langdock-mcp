package main

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testFolderID = "44444444-4444-4444-4444-444444444444"
	testFileID   = "55555555-5555-5555-5555-555555555555"
)

func TestKnowledgeToolsMapToEndpoints(t *testing.T) {
	kb := "/knowledge/" + testFolderID
	tests := []struct {
		tool     string
		args     map[string]any
		method   string
		path     string
		query    string
		wantBody string
	}{
		{"list_knowledge_bases", map[string]any{}, "GET", "/knowledge", "", ""},
		{"list_knowledge_bases", map[string]any{"limit": 20, "cursor": "abc"}, "GET", "/knowledge", "cursor=abc&limit=20", ""},
		{"update_knowledge_base", map[string]any{"folderId": testFolderID, "name": "Docs"}, "PATCH", kb + "/folder", "", `{"name":"Docs"}`},
		{"update_knowledge_base", map[string]any{"folderId": testFolderID, "description": ""}, "PATCH", kb + "/folder", "", `{"description":""}`},
		{"list_knowledge_files", map[string]any{"folderId": testFolderID}, "GET", kb + "/list", "", ""},
		{"get_knowledge_file", map[string]any{"folderId": testFolderID, "attachmentId": testFileID}, "GET", kb + "/" + testFileID, "", ""},
		{"delete_knowledge_file", map[string]any{"folderId": testFolderID, "attachmentId": testFileID}, "DELETE", kb + "/" + testFileID, "", ""},
		{"search_knowledge", map[string]any{"query": "vacation policy"}, "POST", "/knowledge/search", "", `{"query":"vacation policy"}`},
		{"grant_knowledge_access", map[string]any{"folderId": testFolderID, "targetIds": []any{"u1", "k1"}, "role": "EDITOR"}, "POST", kb + "/access", "", `{"targetIds":["u1","k1"],"role":"EDITOR"}`},
		{"update_knowledge_access", map[string]any{"folderId": testFolderID, "type": "API_KEY", "targetId": "k1", "role": "USER"}, "PATCH", kb + "/access", "", `{"type":"API_KEY","targetId":"k1","role":"USER"}`},
		{"revoke_knowledge_access", map[string]any{"folderId": testFolderID, "type": "USER", "targetId": "u1"}, "DELETE", kb + "/access", "", `{"type":"USER","targetId":"u1"}`},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			cs, fake := connect(t, "secret", nil)
			text, isErr := callTool(t, cs, tt.tool, tt.args)
			if isErr {
				t.Fatalf("tool error: %s", text)
			}
			req := fake.last(t)
			if req.Method != tt.method || req.Path != tt.path || req.Query != tt.query {
				t.Errorf("got %s %s?%s, want %s %s?%s", req.Method, req.Path, req.Query, tt.method, tt.path, tt.query)
			}
			if req.Auth != "Bearer secret" {
				t.Errorf("Authorization = %q", req.Auth)
			}
			if tt.wantBody != "" && req.ContentType != "application/json" {
				t.Errorf("Content-Type = %q", req.ContentType)
			}
			assertJSONEqual(t, req.Body, tt.wantBody)
		})
	}
}

type multipartUpload struct {
	fields      map[string]string
	fileName    string
	contentType string
	data        []byte
}

func parseUpload(t *testing.T, req recorded) multipartUpload {
	t.Helper()
	mt, params, err := mime.ParseMediaType(req.ContentType)
	if err != nil || mt != "multipart/form-data" {
		t.Fatalf("Content-Type %q: %v", req.ContentType, err)
	}
	u := multipartUpload{fields: map[string]string{}}
	mr := multipart.NewReader(strings.NewReader(string(req.Body)), params["boundary"])
	files := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(part)
		if part.FileName() == "" {
			u.fields[part.FormName()] = string(data)
			continue
		}
		if part.FormName() != "file" {
			t.Errorf("file part named %q", part.FormName())
		}
		files++
		u.fileName, u.contentType, u.data = part.FileName(), part.Header.Get("Content-Type"), data
	}
	if files != 1 {
		t.Fatalf("got %d file parts, want 1", files)
	}
	return u
}

func TestKnowledgeFileUploads(t *testing.T) {
	dir := t.TempDir()
	docx := filepath.Join(dir, "Handbook.DOCX")
	md := filepath.Join(dir, "notes.md")
	dotx := filepath.Join(dir, "letter.dotx")
	os.WriteFile(dotx, []byte("PK\x03\x04word"), 0o600)
	os.WriteFile(docx, []byte("PK\x03\x04word"), 0o600)
	os.WriteFile(md, []byte("# Notes\n"), 0o600)
	const ooxml = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

	tests := []struct {
		name       string
		tool       string
		args       map[string]any
		method     string
		file       string
		wantType   string
		wantFields map[string]string
	}{
		{"upload", "upload_knowledge_file", map[string]any{"folderId": testFolderID, "filePath": docx}, "POST", docx, ooxml, map[string]string{}},
		{"upload template", "upload_knowledge_file", map[string]any{"folderId": testFolderID, "filePath": dotx}, "POST", dotx, "application/vnd.openxmlformats-officedocument.wordprocessingml.template", map[string]string{}},
		{"upload with url", "upload_knowledge_file", map[string]any{"folderId": testFolderID, "filePath": md, "url": "https://example.com/notes"}, "POST", md, "text/markdown", map[string]string{"url": "https://example.com/notes"}},
		{"replace", "replace_knowledge_file", map[string]any{"folderId": testFolderID, "attachmentId": testFileID, "filePath": md}, "PATCH", md, "text/markdown", map[string]string{"attachmentId": testFileID}},
		{"replace with url", "replace_knowledge_file", map[string]any{"folderId": testFolderID, "attachmentId": testFileID, "filePath": docx, "url": "https://example.com/h"}, "PATCH", docx, ooxml, map[string]string{"attachmentId": testFileID, "url": "https://example.com/h"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, fake := connect(t, "secret", nil)
			text, isErr := callTool(t, cs, tt.tool, tt.args)
			if isErr {
				t.Fatalf("tool error: %s", text)
			}
			req := fake.last(t)
			if req.Method != tt.method || req.Path != "/knowledge/"+testFolderID || req.Query != "" {
				t.Errorf("got %s %s?%s", req.Method, req.Path, req.Query)
			}
			if req.Auth != "Bearer secret" {
				t.Errorf("Authorization = %q", req.Auth)
			}
			u := parseUpload(t, req)
			want, _ := os.ReadFile(tt.file)
			if u.fileName != filepath.Base(tt.file) || u.contentType != tt.wantType || string(u.data) != string(want) {
				t.Errorf("file part %q %q %q", u.fileName, u.contentType, u.data)
			}
			if len(u.fields) != len(tt.wantFields) {
				t.Errorf("fields %v, want %v", u.fields, tt.wantFields)
			}
			for k, v := range tt.wantFields {
				if u.fields[k] != v {
					t.Errorf("field %s = %q, want %q", k, u.fields[k], v)
				}
			}
		})
	}
}

func TestKnowledgeLocalValidation(t *testing.T) {
	dir := t.TempDir()
	bigMD := filepath.Join(dir, "big.md")
	f, err := os.Create(bigMD)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Truncate(bigMD, 10<<20+1); err != nil {
		t.Fatal(err)
	}
	blank := "  "
	cases := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"relative path", "upload_knowledge_file", map[string]any{"folderId": "f", "filePath": "docs/a.pdf"}, "absolute"},
		{"missing file", "upload_knowledge_file", map[string]any{"folderId": "f", "filePath": filepath.Join(dir, "nope.pdf")}, "nope.pdf"},
		{"directory", "upload_knowledge_file", map[string]any{"folderId": "f", "filePath": dir}, "not a regular file"},
		{"oversized markdown", "upload_knowledge_file", map[string]any{"folderId": "f", "filePath": bigMD}, "10 MB"},
		{"replace oversized", "replace_knowledge_file", map[string]any{"folderId": "f", "attachmentId": "a", "filePath": bigMD}, "10 MB"},
		{"replace relative", "replace_knowledge_file", map[string]any{"folderId": "f", "attachmentId": "a", "filePath": "a.md"}, "absolute"},
		{"replace empty attachment", "replace_knowledge_file", map[string]any{"folderId": "f", "attachmentId": "", "filePath": bigMD}, "attachmentId"},
		{"update nothing", "update_knowledge_base", map[string]any{"folderId": "f"}, "nothing to update"},
		{"update blank name", "update_knowledge_base", map[string]any{"folderId": "f", "name": blank}, "blank"},
		{"limit 0", "list_knowledge_bases", map[string]any{"limit": 0}, ""},
		{"limit 101", "list_knowledge_bases", map[string]any{"limit": 101}, ""},
		{"empty name", "update_knowledge_base", map[string]any{"folderId": "f", "name": ""}, ""},
		{"empty folderId", "list_knowledge_files", map[string]any{"folderId": ""}, ""},
		{"empty attachmentId", "delete_knowledge_file", map[string]any{"folderId": "f", "attachmentId": ""}, ""},
		{"grant bad role", "grant_knowledge_access", map[string]any{"folderId": "f", "targetIds": []any{"u"}, "role": "OWNER"}, ""},
		{"grant no targets", "grant_knowledge_access", map[string]any{"folderId": "f", "targetIds": []any{}, "role": "USER"}, ""},
		{"update bad type", "update_knowledge_access", map[string]any{"folderId": "f", "type": "GROUP", "targetId": "g", "role": "USER"}, ""},
		{"update bad role", "update_knowledge_access", map[string]any{"folderId": "f", "type": "USER", "targetId": "u", "role": "ADMIN"}, ""},
		{"revoke bad type", "revoke_knowledge_access", map[string]any{"folderId": "f", "type": "GROUP", "targetId": "g"}, ""},
		{"empty query", "search_knowledge", map[string]any{"query": ""}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cs, fake := connect(t, "k", nil)
			text, isErr := callTool(t, cs, c.tool, c.args)
			if !isErr || !strings.Contains(text, c.want) {
				t.Fatalf("got %v %q, want error containing %q", isErr, text, c.want)
			}
			if len(fake.requests) != 0 {
				t.Fatal("invalid input reached the API")
			}
		})
	}
}

func TestKnowledgeFileLimits(t *testing.T) {
	for ext, want := range map[string]int64{
		".txt": 10 << 20, ".md": 10 << 20, ".json": 10 << 20, ".vtt": 10 << 20,
		".xml": 30 << 20, ".pdf": 256 << 20, ".docx": 256 << 20,
	} {
		if got := knowledgeFileLimit(ext); got != want {
			t.Errorf("knowledgeFileLimit(%q) = %d, want %d", ext, got, want)
		}
	}
}

func TestKnowledgeErrorHints(t *testing.T) {
	cs, _ := connect(t, "k", map[string]fakeResponse{
		"GET /knowledge":                           {http.StatusForbidden, `{"message":"denied"}`},
		"POST /knowledge/" + testFolderID:          {http.StatusRequestEntityTooLarge, `{"message":"too big"}`},
		"GET /knowledge/" + testFolderID + "/list": {http.StatusNotFound, ``},
	})
	text, isErr := callTool(t, cs, "list_knowledge_bases", map[string]any{"limit": 5})
	if !isErr || !strings.Contains(text, "403") || !strings.Contains(text, "KNOWLEDGE_FOLDER_API") || !strings.Contains(text, "denied") {
		t.Errorf("403: got %v %q", isErr, text)
	}
	path := filepath.Join(t.TempDir(), "a.pdf")
	os.WriteFile(path, []byte("%PDF-1.7"), 0o600)
	text, isErr = callTool(t, cs, "upload_knowledge_file", map[string]any{"folderId": testFolderID, "filePath": path})
	if !isErr || !strings.Contains(text, "413") || !strings.Contains(text, "size limit") || !strings.Contains(text, "too big") {
		t.Errorf("413: got %v %q", isErr, text)
	}
	text, isErr = callTool(t, cs, "list_knowledge_files", map[string]any{"folderId": testFolderID})
	if !isErr || !strings.Contains(text, "knowledge base") {
		t.Errorf("404: got %v %q", isErr, text)
	}
}

func TestAgentRoutesKeepAgentHints(t *testing.T) {
	cs, _ := connect(t, "k", map[string]fakeResponse{
		"POST /agent/v1/publish": {http.StatusConflict, ``},
		"GET /agent/v1/get":      {http.StatusForbidden, ``},
	})
	text, isErr := callTool(t, cs, "publish_agent", map[string]any{"agentId": testAgentID})
	if !isErr || !strings.Contains(text, "no changes to publish") {
		t.Errorf("409: got %v %q", isErr, text)
	}
	text, isErr = callTool(t, cs, "get_agent", map[string]any{"agentId": testAgentID})
	if !isErr || !strings.Contains(text, "Agent API scope") || strings.Contains(text, "KNOWLEDGE_FOLDER_API") {
		t.Errorf("403: got %v %q", isErr, text)
	}
}

func TestFamilyOf(t *testing.T) {
	for path, want := range map[string]apiFamily{
		"/knowledge":                  knowledgeAPI,
		"/knowledge?limit=1":          knowledgeAPI,
		"/knowledge/search":           knowledgeAPI,
		"/knowledge/f/a":              knowledgeAPI,
		"/knowledgebase":              integrationsAPI,
		"/agent/v1/get?agentId=x":     agentsAPI,
		"/agent/v1/update":            agentsAPI,
		"/integrations/v1/x":          integrationsAPI,
		"/integrations/v1/knowledge/": integrationsAPI,
	} {
		if got := familyOf(path); got != want {
			t.Errorf("familyOf(%q) = %d, want %d", path, got, want)
		}
	}
}

func TestKnowledgeToolsListed(t *testing.T) {
	cs, _ := connect(t, "k", nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, tool := range res.Tools {
		listed[tool.Name] = true
	}
	for _, name := range []string{
		"list_knowledge_bases", "update_knowledge_base", "list_knowledge_files", "get_knowledge_file",
		"upload_knowledge_file", "replace_knowledge_file", "delete_knowledge_file", "search_knowledge",
		"grant_knowledge_access", "update_knowledge_access", "revoke_knowledge_access",
	} {
		if !listed[name] {
			t.Errorf("%s missing from tools/list", name)
		}
	}
}
