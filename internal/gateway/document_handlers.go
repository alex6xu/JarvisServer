package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func documentUploadRequestMaxBytes(opts Options) int64 {
	// Multipart framing and headers need a small allowance beyond file content.
	return opts.DocumentUploadMaxBytes + (1 << 20)
}

func cleanDocumentFilename(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimSpace(filepath.Base(name))
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, 0) || len([]byte(name)) > 255 || !utf8.ValidString(name) {
		return "", errors.New("invalid filename")
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("invalid filename")
	}
	return name, nil
}

func (s *Service) handleUploadProjectDocument(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	projectID := strings.TrimSpace(pathParam(r, "projectId"))
	if _, err := s.Audit.ProjectByID(r.Context(), accountID, projectID); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, documentUploadRequestMaxBytes(s.Opts))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "invalid or oversized multipart upload")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	filename, err := cleanDocumentFilename(header.Filename)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if header.Size <= 0 {
		writeErr(w, http.StatusBadRequest, "empty documents are not allowed")
		return
	}
	if header.Size > s.Opts.DocumentUploadMaxBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "document exceeds upload limit")
		return
	}
	usage, err := s.Audit.ProjectDocumentUsage(r.Context(), accountID, projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to check document quota")
		return
	}
	if usage+header.Size > s.Opts.DocumentProjectMaxBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "project document quota exceeded")
		return
	}

	sniff := make([]byte, 512)
	n, readErr := io.ReadFull(file, sniff)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		writeErr(w, http.StatusBadRequest, "failed to read document")
		return
	}
	sniff = sniff[:n]
	format, err := documentFormatFor(filename, header.Header.Get("Content-Type"), sniff)
	if err != nil {
		writeErr(w, http.StatusUnsupportedMediaType, err.Error())
		return
	}

	documentID := newID("doc")
	relDir, err := documentRelativeDir(accountID, projectID, documentID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to allocate document storage")
		return
	}
	dir, err := createDocumentDir(s.Opts.DocumentsRoot, relDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create document storage")
		return
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()

	hasher := sha256.New()
	source := io.MultiReader(bytes.NewReader(sniff), file)
	size, err := writeDocumentAtomic(filepath.Join(dir, "original"), io.TeeReader(source, hasher), s.Opts.DocumentUploadMaxBytes)
	if err != nil {
		if size > s.Opts.DocumentUploadMaxBytes {
			writeErr(w, http.StatusRequestEntityTooLarge, "document exceeds upload limit")
		} else {
			writeErr(w, http.StatusBadRequest, "failed to store document")
		}
		return
	}
	if size == 0 {
		writeErr(w, http.StatusBadRequest, "empty documents are not allowed")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	doc := ProjectDocument{
		ID: documentID, AccountID: accountID, ProjectID: projectID, Filename: filename,
		MIMEType: format.MIMEType, SizeBytes: size, SHA256: hex.EncodeToString(hasher.Sum(nil)),
		Status: DocumentStatusProcessing, StoragePath: filepath.Join(relDir, "original"),
		MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Audit.CreateProjectDocument(r.Context(), doc); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to record document")
		return
	}
	cleanup = false

	parseCtx, cancel := context.WithTimeout(r.Context(), s.Opts.DocumentParserTimeout)
	defer cancel()
	type result struct {
		text []byte
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		text, err := extractDocumentText(filepath.Join(dir, "original"), format, s.Opts.DocumentExtractedTextMaxBytes)
		resultCh <- result{text, err}
	}()
	var extracted []byte
	select {
	case parsed := <-resultCh:
		extracted, err = parsed.text, parsed.err
	case <-parseCtx.Done():
		err = errors.New("parser timeout")
	}
	if err != nil {
		_ = s.Audit.FailProjectDocument(context.Background(), accountID, projectID, documentID, "parse_failed")
		doc, _ = s.Audit.ProjectDocumentByID(context.Background(), accountID, projectID, documentID, false)
		writeJSON(w, http.StatusCreated, map[string]any{"document": doc})
		return
	}
	extractedPath := filepath.Join(dir, "extracted.txt")
	if _, err := writeDocumentAtomic(extractedPath, strings.NewReader(string(extracted)), s.Opts.DocumentExtractedTextMaxBytes); err != nil {
		_ = s.Audit.FailProjectDocument(context.Background(), accountID, projectID, documentID, "extract_store_failed")
		doc, _ = s.Audit.ProjectDocumentByID(context.Background(), accountID, projectID, documentID, false)
		writeJSON(w, http.StatusCreated, map[string]any{"document": doc})
		return
	}
	extractedRel := filepath.Join(relDir, "extracted.txt")
	if err := s.Audit.CompleteProjectDocument(r.Context(), accountID, projectID, documentID, extractedRel, int64(len(extracted)), format.Parser); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to complete document")
		return
	}
	doc, _ = s.Audit.ProjectDocumentByID(r.Context(), accountID, projectID, documentID, false)
	writeJSON(w, http.StatusCreated, map[string]any{"document": doc})
}

func (s *Service) handleListProjectDocuments(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	docs, err := s.Audit.ListProjectDocuments(r.Context(), accountID, pathParam(r, "projectId"), ProjectDocumentListOptions{Status: strings.TrimSpace(r.URL.Query().Get("status")), Limit: limit, Cursor: strings.TrimSpace(r.URL.Query().Get("cursor"))})
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list documents")
		return
	}
	var next string
	if len(docs) > 0 && limit > 0 && len(docs) == limit {
		next = docs[len(docs)-1].CreatedAt
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": docs, "next_cursor": next})
}

func (s *Service) handleGetProjectDocument(w http.ResponseWriter, r *http.Request) {
	doc, ok := s.requestProjectDocument(w, r, false)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"document": doc})
}

func (s *Service) requestProjectDocument(w http.ResponseWriter, r *http.Request, readyOnly bool) (ProjectDocument, bool) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return ProjectDocument{}, false
	}
	doc, err := s.Audit.ProjectDocumentByID(r.Context(), accountID, pathParam(r, "projectId"), pathParam(r, "documentId"), false)
	if err != nil || (readyOnly && doc.Status != DocumentStatusReady) {
		writeErr(w, http.StatusNotFound, "document not found")
		return ProjectDocument{}, false
	}
	return doc, true
}

func (s *Service) handleDownloadProjectDocument(w http.ResponseWriter, r *http.Request) {
	doc, ok := s.requestProjectDocument(w, r, true)
	if !ok {
		return
	}
	path, err := resolveDocumentPath(s.Opts.DocumentsRoot, doc.StoragePath)
	if err != nil {
		writeErr(w, http.StatusNotFound, "document not found")
		return
	}
	file, err := openDocumentFile(path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "document not found")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", doc.MIMEType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": doc.Filename}))
	w.Header().Set("Content-Length", strconv.FormatInt(doc.SizeBytes, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func (s *Service) handleDeleteProjectDocument(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	doc, err := s.Audit.DeleteProjectDocument(r.Context(), accountID, pathParam(r, "projectId"), pathParam(r, "documentId"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "document not found")
		return
	}
	if relDir, err := documentRelativeDir(accountID, doc.ProjectID, doc.ID); err == nil {
		if path, err := resolveDocumentPath(s.Opts.DocumentsRoot, relDir); err == nil {
			_ = os.RemoveAll(path)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleProjectDocumentLimits(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"upload_max_bytes": s.Opts.DocumentUploadMaxBytes, "project_max_bytes": s.Opts.DocumentProjectMaxBytes, "extracted_text_max_bytes": s.Opts.DocumentExtractedTextMaxBytes, "allowed_extensions": []string{".txt", ".md", ".csv", ".json", ".docx", ".xlsx"}})
}
