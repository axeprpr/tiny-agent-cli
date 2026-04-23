package tools

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"rsc.io/pdf"

	"tiny-agent-cli/internal/model"
)

type inspectDOCXTool struct {
	workDir string
}

type inspectPDFTool struct {
	workDir string
}

type checkWebappTool struct {
	client *http.Client
}

func newInspectDOCXTool(workDir string) Tool {
	return &inspectDOCXTool{workDir: workDir}
}

func newInspectPDFTool(workDir string) Tool {
	return &inspectPDFTool{workDir: workDir}
}

func newCheckWebappTool() Tool {
	return &checkWebappTool{client: &http.Client{Timeout: 20 * time.Second}}
}

func (t *inspectDOCXTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "inspect_docx",
			Description: "Extract structure and text from a DOCX file inside the workspace.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"path"},
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "DOCX path relative to the workspace root.",
					},
					"max_paragraphs": map[string]any{
						"type":        "integer",
						"description": "Maximum paragraphs to include in the preview, default 12.",
					},
				},
			},
		},
	}
}

func (t *inspectDOCXTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Path          string `json:"path"`
		MaxParagraphs int    `json:"max_paragraphs"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	path, err := securePath(t.workDir, args.Path)
	if err != nil {
		return "", err
	}
	if args.MaxParagraphs <= 0 {
		args.MaxParagraphs = 12
	}
	doc, err := inspectDOCX(path, args.MaxParagraphs)
	if err != nil {
		return "", err
	}
	return renderDOCXInspection(args.Path, doc), nil
}

func (t *inspectPDFTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "inspect_pdf",
			Description: "Extract metadata and text preview from a PDF file inside the workspace.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"path"},
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "PDF path relative to the workspace root.",
					},
					"max_pages": map[string]any{
						"type":        "integer",
						"description": "Maximum number of pages to inspect, default 3.",
					},
					"max_chars": map[string]any{
						"type":        "integer",
						"description": "Maximum extracted text characters to include, default 2400.",
					},
				},
			},
		},
	}
}

func (t *inspectPDFTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Path     string `json:"path"`
		MaxPages int    `json:"max_pages"`
		MaxChars int    `json:"max_chars"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	path, err := securePath(t.workDir, args.Path)
	if err != nil {
		return "", err
	}
	if args.MaxPages <= 0 {
		args.MaxPages = 3
	}
	if args.MaxChars <= 0 {
		args.MaxChars = 2400
	}
	info, err := inspectPDF(path, args.MaxPages, args.MaxChars)
	if err != nil {
		return "", err
	}
	return renderPDFInspection(args.Path, info), nil
}

func (t *checkWebappTool) Definition() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.FunctionSpec{
			Name:        "check_webapp",
			Description: "Fetch a web page and verify basic user-visible expectations such as status, title, and required text.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"url"},
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "HTTP or HTTPS URL to verify.",
					},
					"expected_status": map[string]any{
						"type":        "integer",
						"description": "Optional expected HTTP status code, default 200.",
					},
					"title_contains": map[string]any{
						"type":        "string",
						"description": "Optional substring expected in the HTML title.",
					},
					"contains_text": map[string]any{
						"type":        "array",
						"description": "Optional list of visible text snippets that must appear in the page body.",
						"items": map[string]any{
							"type": "string",
						},
					},
					"max_bytes": map[string]any{
						"type":        "integer",
						"description": "Optional max response bytes to read, default 131072.",
					},
				},
			},
		},
	}
}

func (t *checkWebappTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		URL            string   `json:"url"`
		ExpectedStatus int      `json:"expected_status"`
		TitleContains  string   `json:"title_contains"`
		ContainsText   []string `json:"contains_text"`
		MaxBytes       int64    `json:"max_bytes"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return "", fmt.Errorf("url is required")
	}
	if args.ExpectedStatus == 0 {
		args.ExpectedStatus = http.StatusOK
	}
	if args.MaxBytes <= 0 {
		args.MaxBytes = 128 * 1024
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tiny-agent-cli/0.1")
	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, args.MaxBytes))
	if err != nil {
		return "", err
	}
	page := inspectFetchedHTML(string(body))
	if resp.StatusCode != args.ExpectedStatus {
		return "", fmt.Errorf("expected status %d, got %d", args.ExpectedStatus, resp.StatusCode)
	}
	if strings.TrimSpace(args.TitleContains) != "" && !strings.Contains(strings.ToLower(page.Title), strings.ToLower(strings.TrimSpace(args.TitleContains))) {
		return "", fmt.Errorf("expected title to contain %q, got %q", args.TitleContains, page.Title)
	}
	for _, want := range args.ContainsText {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		if !strings.Contains(strings.ToLower(page.Text), strings.ToLower(want)) {
			return "", fmt.Errorf("expected page to contain %q", want)
		}
	}
	return renderWebappCheck(url, resp.Status, page), nil
}

type docxInspection struct {
	Title          string
	Creator        string
	Paragraphs     []string
	Headings       []string
	TableCount     int
	ImageCount     int
	ParagraphCount int
}

type pdfInspection struct {
	Title      string
	Author     string
	Subject    string
	PageCount  int
	PageSample []string
}

func inspectDOCX(path string, maxParagraphs int) (docxInspection, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return docxInspection{}, fmt.Errorf("open docx: %w", err)
	}
	defer reader.Close()

	files := make(map[string]*zip.File, len(reader.File))
	images := 0
	for _, file := range reader.File {
		files[file.Name] = file
		if strings.HasPrefix(strings.ToLower(file.Name), "word/media/") {
			images++
		}
	}

	var out docxInspection
	out.ImageCount = images
	if core := files["docProps/core.xml"]; core != nil {
		title, creator, _ := readDOCXCore(core)
		out.Title = title
		out.Creator = creator
	}
	docFile := files["word/document.xml"]
	if docFile == nil {
		return docxInspection{}, fmt.Errorf("word/document.xml not found")
	}
	paragraphs, headings, tables, err := readDOCXDocument(docFile, maxParagraphs)
	if err != nil {
		return docxInspection{}, fmt.Errorf("parse docx document: %w", err)
	}
	out.Paragraphs = paragraphs
	out.ParagraphCount = len(paragraphs)
	out.Headings = headings
	out.TableCount = tables
	return out, nil
}

func readDOCXCore(file *zip.File) (string, string, error) {
	rc, err := file.Open()
	if err != nil {
		return "", "", err
	}
	defer rc.Close()
	var core struct {
		Title   string `xml:"title"`
		Creator string `xml:"creator"`
	}
	if err := xml.NewDecoder(rc).Decode(&core); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(core.Title), strings.TrimSpace(core.Creator), nil
}

func readDOCXDocument(file *zip.File, maxParagraphs int) ([]string, []string, int, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, nil, 0, err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var (
		inParagraph bool
		inText      bool
		style       string
		textBuilder strings.Builder
		paragraphs  []string
		headings    []string
		tables      int
	)
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, 0, err
		}
		switch el := tok.(type) {
		case xml.StartElement:
			switch el.Name.Local {
			case "p":
				inParagraph = true
				style = ""
				textBuilder.Reset()
			case "t":
				if inParagraph {
					inText = true
				}
			case "pStyle":
				for _, attr := range el.Attr {
					if attr.Name.Local == "val" {
						style = strings.TrimSpace(attr.Value)
						break
					}
				}
			case "tbl":
				tables++
			}
		case xml.EndElement:
			switch el.Name.Local {
			case "t":
				inText = false
			case "p":
				if inParagraph {
					text := strings.Join(strings.Fields(strings.TrimSpace(textBuilder.String())), " ")
					if text != "" {
						paragraphs = append(paragraphs, text)
						if len(paragraphs) <= maxParagraphs && strings.HasPrefix(strings.ToLower(style), "heading") {
							headings = append(headings, text)
						}
					}
					inParagraph = false
				}
			}
		case xml.CharData:
			if inParagraph && inText {
				textBuilder.WriteString(string(el))
			}
		}
	}
	if len(paragraphs) > maxParagraphs {
		paragraphs = paragraphs[:maxParagraphs]
	}
	if len(headings) > maxParagraphs {
		headings = headings[:maxParagraphs]
	}
	return paragraphs, headings, tables, nil
}

func renderDOCXInspection(path string, doc docxInspection) string {
	lines := []string{"path: " + path}
	if doc.Title != "" {
		lines = append(lines, "title: "+doc.Title)
	}
	if doc.Creator != "" {
		lines = append(lines, "creator: "+doc.Creator)
	}
	lines = append(lines,
		fmt.Sprintf("paragraph_count: %d", doc.ParagraphCount),
		fmt.Sprintf("table_count: %d", doc.TableCount),
		fmt.Sprintf("image_count: %d", doc.ImageCount),
	)
	if len(doc.Headings) > 0 {
		lines = append(lines, "headings:")
		for _, item := range doc.Headings {
			lines = append(lines, "- "+item)
		}
	}
	if len(doc.Paragraphs) > 0 {
		lines = append(lines, "paragraph_preview:")
		for _, item := range doc.Paragraphs {
			lines = append(lines, "- "+compactPreviewString(item, 200))
		}
	}
	return strings.Join(lines, "\n")
}

func inspectPDF(path string, maxPages, maxChars int) (pdfInspection, error) {
	reader, err := pdf.Open(path)
	if err != nil {
		return pdfInspection{}, fmt.Errorf("open pdf: %w", err)
	}
	out := pdfInspection{
		PageCount: reader.NumPage(),
	}
	info := reader.Trailer().Key("Info")
	out.Title = strings.TrimSpace(info.Key("Title").Text())
	out.Author = strings.TrimSpace(info.Key("Author").Text())
	out.Subject = strings.TrimSpace(info.Key("Subject").Text())
	pageLimit := min(maxPages, out.PageCount)
	remainingChars := maxChars
	for i := 1; i <= pageLimit; i++ {
		page := reader.Page(i)
		content := page.Content()
		sort.Sort(pdf.TextHorizontal(content.Text))
		var b strings.Builder
		for _, text := range content.Text {
			part := strings.Join(strings.Fields(strings.TrimSpace(text.S)), " ")
			if part == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(part)
			if b.Len() >= remainingChars {
				break
			}
		}
		sample := strings.TrimSpace(b.String())
		if sample == "" {
			sample = "(no extractable text)"
		}
		sample = compactPreviewString(sample, remainingChars)
		out.PageSample = append(out.PageSample, fmt.Sprintf("page %d: %s", i, sample))
		remainingChars -= len(sample)
		if remainingChars <= 0 {
			break
		}
	}
	return out, nil
}

func renderPDFInspection(path string, info pdfInspection) string {
	lines := []string{
		"path: " + path,
		fmt.Sprintf("page_count: %d", info.PageCount),
	}
	if info.Title != "" {
		lines = append(lines, "title: "+info.Title)
	}
	if info.Author != "" {
		lines = append(lines, "author: "+info.Author)
	}
	if info.Subject != "" {
		lines = append(lines, "subject: "+info.Subject)
	}
	if len(info.PageSample) > 0 {
		lines = append(lines, "text_preview:")
		lines = append(lines, info.PageSample...)
	}
	return strings.Join(lines, "\n")
}

type fetchedHTML struct {
	Title string
	Text  string
}

var htmlTitlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]+>`)

func inspectFetchedHTML(body string) fetchedHTML {
	title := ""
	if match := htmlTitlePattern.FindStringSubmatch(body); len(match) > 1 {
		title = strings.Join(strings.Fields(html.UnescapeString(match[1])), " ")
	}
	text := htmlTagPattern.ReplaceAllString(body, " ")
	text = html.UnescapeString(text)
	text = strings.Join(strings.Fields(text), " ")
	return fetchedHTML{
		Title: strings.TrimSpace(title),
		Text:  strings.TrimSpace(text),
	}
}

func renderWebappCheck(url, status string, page fetchedHTML) string {
	lines := []string{
		"status: " + status,
		"url: " + url,
	}
	if page.Title != "" {
		lines = append(lines, "title: "+page.Title)
	}
	if page.Text != "" {
		lines = append(lines, "text_preview: "+compactPreviewString(page.Text, 240))
	}
	return strings.Join(lines, "\n")
}
