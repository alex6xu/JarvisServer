package gateway

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectDocumentMigrationAndStoreIsolation(t *testing.T) {
	store := newTestGatewayStore(t)
	if err := applyGatewayMigrations(store.db); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
	var migrationCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=22`).Scan(&migrationCount); err != nil || migrationCount != 1 {
		t.Fatalf("migration count=%d err=%v", migrationCount, err)
	}
	first, _ := store.CreateAccount(context.Background(), "document-first", "", "user", "password")
	second, _ := store.CreateAccount(context.Background(), "document-second", "", "user", "password")
	project, _ := store.CreateProject(context.Background(), first.ID, "Documents", "")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	doc := ProjectDocument{ID: "doc_store", AccountID: first.ID, ProjectID: project.ID, Filename: "readme.md", MIMEType: "text/markdown", SizeBytes: 5, SHA256: strings.Repeat("a", 64), Status: DocumentStatusReady, StoragePath: "1/x/original", ExtractedTextPath: "1/x/extracted.txt", MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateProjectDocument(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProjectDocumentByID(context.Background(), second.ID, project.ID, doc.ID, false); err == nil {
		t.Fatal("foreign account read document")
	}
	otherProject, _ := store.CreateProject(context.Background(), first.ID, "Other", "")
	if _, err := store.ProjectDocumentByID(context.Background(), first.ID, otherProject.ID, doc.ID, false); err == nil {
		t.Fatal("cross-project read document")
	}
	deleted, err := store.DeleteProjectDocument(context.Background(), first.ID, project.ID, doc.ID)
	if err != nil || deleted.Status != DocumentStatusDeleted {
		t.Fatalf("delete=%+v err=%v", deleted, err)
	}
	if _, err := store.ProjectDocumentByID(context.Background(), first.ID, project.ID, doc.ID, false); err == nil {
		t.Fatal("deleted document remained readable")
	}
}

func TestDocumentTextExtractors(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ name, body, parser, want string }{
		{"notes.txt", "hello 世界", "text", "hello 世界"},
		{"table.csv", "name,value\na,1\n", "csv", "a,1"},
		{"data.json", `{"answer":42}`, "json", `"answer": 42`},
	}
	for _, tc := range cases {
		t.Run(tc.parser, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := extractDocumentText(path, documentFormat{Parser: tc.parser}, 1<<20)
			if err != nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("out=%q err=%v", out, err)
			}
		})
	}

	docx := filepath.Join(dir, "sample.docx")
	writeTestOfficeZIP(t, docx, map[string]string{
		"[Content_Types].xml": `<Types/>`,
		"word/document.xml":   `<w:document xmlns:w="w"><w:body><w:p><w:r><w:t>Hello DOCX</w:t></w:r></w:p></w:body></w:document>`,
	})
	out, err := extractDocumentText(docx, documentFormat{Parser: "docx"}, 1<<20)
	if err != nil || !strings.Contains(string(out), "Hello DOCX") {
		t.Fatalf("docx=%q err=%v", out, err)
	}

	xlsx := filepath.Join(dir, "sample.xlsx")
	writeTestOfficeZIP(t, xlsx, map[string]string{
		"[Content_Types].xml":      `<Types/>`,
		"xl/sharedStrings.xml":     `<sst><si><t>Hello XLSX</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row><c r="A1" t="s"><v>0</v></c><c r="B1"><f>1+1</f><v>2</v></c></row></sheetData></worksheet>`,
	})
	out, err = extractDocumentText(xlsx, documentFormat{Parser: "xlsx"}, 1<<20)
	if err != nil || !strings.Contains(string(out), "A1\tHello XLSX") || strings.Contains(string(out), "1+1") {
		t.Fatalf("xlsx=%q err=%v", out, err)
	}
}

func TestOfficeExtractorRejectsExternalRelationship(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.docx")
	writeTestOfficeZIP(t, path, map[string]string{
		"word/document.xml":            `<document><p><t>text</t></p></document>`,
		"word/_rels/document.xml.rels": `<Relationships><Relationship TargetMode="External" Target="https://example.com"/></Relationships>`,
	})
	if _, err := extractDocumentText(path, documentFormat{Parser: "docx"}, 1<<20); err == nil {
		t.Fatal("external relationship accepted")
	}
}

func TestProjectDocumentUploadListDownloadDelete(t *testing.T) {
	root := t.TempDir()
	svc, err := NewService(Options{Cwd: root, DatabasePath: filepath.Join(root, "gateway.db"), DocumentsRoot: filepath.Join(root, "documents"), AuthMode: "none", AdminPassword: "password", NoTools: true, DocumentUploadMaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	accounts, _ := svc.Audit.ListAccounts(context.Background())
	account := accounts[0]
	project, err := svc.Audit.CreateProject(context.Background(), account.ID, "Upload", "")
	if err != nil {
		t.Fatal(err)
	}

	request := multipartDocumentRequest(t, project.ID, "spec.md", "text/markdown", []byte("# Safe document\nbody"))
	request = request.WithContext(context.WithValue(request.Context(), accountContextKey{}, account))
	response := httptest.NewRecorder()
	svc.handleUploadProjectDocument(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", response.Code, response.Body.String())
	}
	var uploaded struct {
		Document ProjectDocument `json:"document"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.Document.Status != DocumentStatusReady || uploaded.Document.StoragePath != "" {
		t.Fatalf("uploaded=%+v", uploaded.Document)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/projects/x/documents", nil)
	listReq.SetPathValue("projectId", project.ID)
	listReq = listReq.WithContext(context.WithValue(listReq.Context(), accountContextKey{}, account))
	listRes := httptest.NewRecorder()
	svc.handleListProjectDocuments(listRes, listReq)
	if listRes.Code != http.StatusOK || !strings.Contains(listRes.Body.String(), uploaded.Document.ID) {
		t.Fatalf("list=%d %s", listRes.Code, listRes.Body.String())
	}

	downloadReq := documentRequest(account, project.ID, uploaded.Document.ID, http.MethodGet)
	downloadRes := httptest.NewRecorder()
	svc.handleDownloadProjectDocument(downloadRes, downloadReq)
	if downloadRes.Code != http.StatusOK || downloadRes.Body.String() != "# Safe document\nbody" || strings.Contains(downloadRes.Header().Get("Content-Disposition"), "\r") {
		t.Fatalf("download=%d %q headers=%v", downloadRes.Code, downloadRes.Body.String(), downloadRes.Header())
	}

	deleteReq := documentRequest(account, project.ID, uploaded.Document.ID, http.MethodDelete)
	deleteRes := httptest.NewRecorder()
	svc.handleDeleteProjectDocument(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete=%d %s", deleteRes.Code, deleteRes.Body.String())
	}
	downloadRes = httptest.NewRecorder()
	svc.handleDownloadProjectDocument(downloadRes, documentRequest(account, project.ID, uploaded.Document.ID, http.MethodGet))
	if downloadRes.Code != http.StatusNotFound {
		t.Fatalf("deleted download status=%d", downloadRes.Code)
	}
}

func TestProjectDocumentUploadLimitsAndType(t *testing.T) {
	root := t.TempDir()
	svc, err := NewService(Options{Cwd: root, DatabasePath: filepath.Join(root, "gateway.db"), DocumentsRoot: filepath.Join(root, "documents"), AuthMode: "none", AdminPassword: "password", NoTools: true, DocumentUploadMaxBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	accounts, _ := svc.Audit.ListAccounts(context.Background())
	account := accounts[0]
	project, _ := svc.Audit.CreateProject(context.Background(), account.ID, "Limits", "")
	for _, tc := range []struct {
		name string
		body []byte
		want int
	}{{"large.txt", []byte("12345"), http.StatusRequestEntityTooLarge}, {"script.exe", []byte("MZ"), http.StatusUnsupportedMediaType}} {
		req := multipartDocumentRequest(t, project.ID, tc.name, "application/octet-stream", tc.body)
		req = req.WithContext(context.WithValue(req.Context(), accountContextKey{}, account))
		res := httptest.NewRecorder()
		svc.handleUploadProjectDocument(res, req)
		if res.Code != tc.want {
			t.Fatalf("%s status=%d body=%s", tc.name, res.Code, res.Body.String())
		}
	}
}

func multipartDocumentRequest(t *testing.T, projectID, filename, contentType string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	header["Content-Type"] = []string{contentType}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/projects/x/documents", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetPathValue("projectId", projectID)
	return req
}

func documentRequest(account Account, projectID, documentID, method string) *http.Request {
	req := httptest.NewRequest(method, "/v1/projects/x/documents/y", nil)
	req.SetPathValue("projectId", projectID)
	req.SetPathValue("documentId", documentID)
	return req.WithContext(context.WithValue(req.Context(), accountContextKey{}, account))
}

func writeTestOfficeZIP(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(entry, strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
