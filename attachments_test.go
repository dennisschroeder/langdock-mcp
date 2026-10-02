package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadAttachment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")
	os.WriteFile(path, []byte("%PDF-1.7"), 0o600)
	cs, fake := connect(t, "secret", nil)
	if text, isErr := callTool(t, cs, "upload_attachment", map[string]any{"filePath": path}); isErr {
		t.Fatalf("tool error: %s", text)
	}
	req := fake.last(t)
	if req.Method != "POST" || req.Path != "/attachment/v1/upload" {
		t.Errorf("got %s %s", req.Method, req.Path)
	}
	u := parseUpload(t, req)
	if u.fileName != "report.pdf" || u.contentType != "application/pdf" || string(u.data) != "%PDF-1.7" || len(u.fields) != 0 {
		t.Errorf("file part %q %q %q %v", u.fileName, u.contentType, u.data, u.fields)
	}
}

func TestUploadAttachmentRejectsRelativePath(t *testing.T) {
	cs, fake := connect(t, "k", nil)
	if text, isErr := callTool(t, cs, "upload_attachment", map[string]any{"filePath": "report.pdf"}); !isErr || !strings.Contains(text, "absolute") {
		t.Errorf("got %v %q", isErr, text)
	}
	if len(fake.requests) != 0 {
		t.Fatal("invalid input reached the API")
	}
}

func TestDeleteAttachment(t *testing.T) {
	cs, fake := connect(t, "k", map[string]fakeResponse{
		"DELETE /attachment/v1/delete": {http.StatusForbidden, `{"message":"no access"}`},
	})
	text, isErr := callTool(t, cs, "delete_attachment", map[string]any{"attachmentId": testAgentID})
	if !isErr || !strings.Contains(text, "KNOWLEDGE_FOLDER_API") || !strings.Contains(text, "no access") {
		t.Errorf("got %v %q", isErr, text)
	}
	assertJSONEqual(t, fake.last(t).Body, `{"attachmentId":"`+testAgentID+`"}`)
	if _, isErr := callTool(t, cs, "delete_attachment", map[string]any{"attachmentId": "../x"}); !isErr {
		t.Error("non-UUID attachmentId must be rejected")
	}
}
