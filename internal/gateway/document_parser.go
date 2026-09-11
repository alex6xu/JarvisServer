package gateway

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxOfficeEntries       = 2000
	maxOfficeExpandedBytes = 50 << 20
	maxOfficeEntryBytes    = 10 << 20
	maxCSVRows             = 10000
	maxCSVColumns          = 256
	maxCSVCellBytes        = 64 << 10
	maxJSONDepth           = 100
)

var errUnsupportedDocument = errors.New("unsupported document type")

type documentFormat struct {
	Extension string
	MIMEType  string
	Parser    string
}

func documentFormatFor(filename, declaredMIME string, sniff []byte) (documentFormat, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	formats := map[string]documentFormat{
		".txt":  {".txt", "text/plain", "text"},
		".md":   {".md", "text/markdown", "markdown"},
		".csv":  {".csv", "text/csv", "csv"},
		".json": {".json", "application/json", "json"},
		".docx": {".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "docx"},
		".xlsx": {".xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"},
	}
	format, ok := formats[ext]
	if !ok {
		return documentFormat{}, errUnsupportedDocument
	}
	declaredMIME = strings.ToLower(strings.TrimSpace(strings.Split(declaredMIME, ";")[0]))
	if declaredMIME != "" && declaredMIME != "application/octet-stream" {
		allowed := map[string][]string{
			".txt": {"text/plain"}, ".md": {"text/markdown", "text/plain"},
			".csv":  {"text/csv", "application/csv", "text/plain"},
			".json": {"application/json", "text/json", "text/plain"},
			".docx": {format.MIMEType, "application/zip"}, ".xlsx": {format.MIMEType, "application/zip"},
		}
		matched := false
		for _, candidate := range allowed[ext] {
			matched = matched || declaredMIME == candidate
		}
		if !matched {
			return documentFormat{}, fmt.Errorf("declared MIME does not match extension")
		}
	}
	if (ext == ".docx" || ext == ".xlsx") && !bytes.HasPrefix(sniff, []byte("PK\x03\x04")) {
		return documentFormat{}, errors.New("Office document is not a ZIP package")
	}
	if ext != ".docx" && ext != ".xlsx" && (bytes.IndexByte(sniff, 0) >= 0 || !utf8.Valid(sniff)) {
		return documentFormat{}, errors.New("text document must be UTF-8 and non-binary")
	}
	return format, nil
}

func extractDocumentText(originalPath string, format documentFormat, maxOutput int64) ([]byte, error) {
	if maxOutput <= 0 {
		maxOutput = 2 << 20
	}
	var text []byte
	var err error
	switch format.Parser {
	case "text", "markdown":
		text, err = readLimitedText(originalPath, maxOutput)
	case "csv":
		text, err = extractCSV(originalPath, maxOutput)
	case "json":
		text, err = extractJSON(originalPath, maxOutput)
	case "docx", "xlsx":
		text, err = extractOffice(originalPath, format.Parser, maxOutput)
	default:
		err = errUnsupportedDocument
	}
	if err != nil {
		return nil, err
	}
	if int64(len(text)) > maxOutput {
		return nil, errors.New("extracted text exceeds limit")
	}
	return text, nil
}

func readLimitedText(path string, max int64) ([]byte, error) {
	data, err := readFileLimit(path, max+1)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("extracted text exceeds limit")
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("invalid UTF-8 text")
	}
	return data, nil
}

func extractCSV(path string, max int64) ([]byte, error) {
	data, err := readFileLimit(path, max+1)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("CSV input exceeds extraction limit")
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true
	var out bytes.Buffer
	writer := csv.NewWriter(&out)
	for row := 0; ; row++ {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid CSV: %w", err)
		}
		if row >= maxCSVRows || len(record) > maxCSVColumns {
			return nil, errors.New("CSV structure exceeds limit")
		}
		for _, cell := range record {
			if len(cell) > maxCSVCellBytes {
				return nil, errors.New("CSV cell exceeds limit")
			}
		}
		if err := writer.Write(record); err != nil {
			return nil, err
		}
		writer.Flush()
		if writer.Error() != nil || int64(out.Len()) > max {
			return nil, errors.New("extracted text exceeds limit")
		}
	}
	return out.Bytes(), nil
}

func extractJSON(path string, max int64) ([]byte, error) {
	data, err := readFileLimit(path, max+1)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("JSON input exceeds extraction limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	depth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("invalid JSON")
		}
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter == '{' || delimiter == '[' {
				depth++
				if depth > maxJSONDepth {
					return nil, errors.New("JSON nesting exceeds limit")
				}
			} else {
				depth--
			}
		}
	}
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		return nil, errors.New("invalid JSON")
	}
	if int64(out.Len()) > max {
		return nil, errors.New("extracted text exceeds limit")
	}
	return out.Bytes(), nil
}

func extractOffice(path, kind string, max int64) ([]byte, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, errors.New("invalid Office package")
	}
	defer zr.Close()
	if len(zr.File) == 0 || len(zr.File) > maxOfficeEntries {
		return nil, errors.New("Office entry count exceeds limit")
	}
	entries := make(map[string]*zip.File, len(zr.File))
	var expanded uint64
	for _, file := range zr.File {
		name := filepath.ToSlash(file.Name)
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "../") || entries[name] != nil || file.FileInfo().Mode()&0o170000 == 0o120000 {
			return nil, errors.New("unsafe Office ZIP entry")
		}
		if file.UncompressedSize64 > maxOfficeEntryBytes {
			return nil, errors.New("Office ZIP entry exceeds limit")
		}
		expanded += file.UncompressedSize64
		if expanded > maxOfficeExpandedBytes || (file.CompressedSize64 > 0 && file.UncompressedSize64 > file.CompressedSize64*100) {
			return nil, errors.New("Office package expansion exceeds limit")
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "vbaproject") || strings.Contains(lower, "activex") || strings.Contains(lower, "embeddings/") || strings.Contains(lower, "oleobject") {
			return nil, errors.New("active Office content is not allowed")
		}
		entries[name] = file
	}
	for name, file := range entries {
		if strings.HasSuffix(strings.ToLower(name), ".rels") {
			data, err := readZipFile(file, maxOfficeEntryBytes)
			if err != nil {
				return nil, err
			}
			if bytes.Contains(bytes.ToLower(data), []byte(`targetmode="external"`)) || bytes.Contains(bytes.ToLower(data), []byte(`targetmode='external'`)) {
				return nil, errors.New("external Office relationships are not allowed")
			}
		}
	}
	if kind == "docx" {
		file := entries["word/document.xml"]
		if file == nil {
			return nil, errors.New("DOCX document.xml is missing")
		}
		return extractXMLText(file, max, map[string]bool{"t": true}, map[string]bool{"p": true, "br": true, "tab": true})
	}
	return extractXLSX(entries, max)
}

func extractXLSX(entries map[string]*zip.File, max int64) ([]byte, error) {
	shared := []string{}
	if file := entries["xl/sharedStrings.xml"]; file != nil {
		data, err := extractXMLText(file, max, map[string]bool{"t": true}, map[string]bool{"si": true})
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			shared = append(shared, strings.TrimSpace(line))
		}
	}
	names := make([]string, 0)
	for name := range entries {
		if strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var out bytes.Buffer
	for _, name := range names {
		fmt.Fprintf(&out, "# %s\n", strings.TrimSuffix(strings.TrimPrefix(name, "xl/worksheets/"), ".xml"))
		if err := extractWorksheet(entries[name], shared, &out, max); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

func extractWorksheet(file *zip.File, shared []string, out *bytes.Buffer, max int64) error {
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(io.LimitReader(rc, maxOfficeEntryBytes+1))
	cellType, cellRef := "", ""
	inValue, inInline := false, false
	var value strings.Builder
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("invalid XLSX XML")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "c":
				cellType, cellRef = "", ""
				for _, a := range t.Attr {
					if a.Name.Local == "t" {
						cellType = a.Value
					}
					if a.Name.Local == "r" {
						cellRef = a.Value
					}
				}
				value.Reset()
			case "v":
				inValue = true
			case "t":
				inInline = true
			}
		case xml.CharData:
			if inValue || inInline {
				value.Write([]byte(t))
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inValue = false
			case "t":
				inInline = false
			case "c":
				v := value.String()
				if cellType == "s" {
					var idx int
					if _, err := fmt.Sscanf(v, "%d", &idx); err == nil && idx >= 0 && idx < len(shared) {
						v = shared[idx]
					}
				}
				if v != "" {
					fmt.Fprintf(out, "%s\t%s\n", cellRef, v)
				}
				if int64(out.Len()) > max {
					return errors.New("extracted text exceeds limit")
				}
			}
		}
	}
	return nil
}

func extractXMLText(file *zip.File, max int64, textElements, breakElements map[string]bool) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(io.LimitReader(rc, maxOfficeEntryBytes+1))
	var out bytes.Buffer
	capture := 0
	depth := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("invalid Office XML")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > 256 {
				return nil, errors.New("Office XML nesting exceeds limit")
			}
			if textElements[t.Name.Local] {
				capture++
			}
		case xml.CharData:
			if capture > 0 {
				out.Write([]byte(t))
			}
		case xml.EndElement:
			if textElements[t.Name.Local] {
				capture--
			}
			if breakElements[t.Name.Local] && (out.Len() == 0 || out.Bytes()[out.Len()-1] != '\n') {
				out.WriteByte('\n')
			}
			depth--
		}
		if int64(out.Len()) > max {
			return nil, errors.New("extracted text exceeds limit")
		}
	}
	return out.Bytes(), nil
}

func readZipFile(file *zip.File, max int64) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, max+1))
}

func readFileLimit(path string, max int64) ([]byte, error) {
	f, err := openDocumentFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max))
}
