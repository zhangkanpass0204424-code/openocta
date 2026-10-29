package tools

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var referencedAttachmentPathPattern = regexp.MustCompile(`(?i)attachments/[A-Za-z0-9._/-]+\.(?:html?|htm)`)

const previewableFileExtensions = `md|markdown|txt|json|ya?ml|csv|log|xml|pdf|html?|htm`

// referencedPathDelimiters matches whitespace, quotes, brackets, backtick, ASCII/Chinese colon.
var referencedPathDelimiters = `(?:^|[\s"'(\[` + "`" + `:：])`

// Matches sandbox-relative previewable files mentioned in assistant text (README.md, docs/guide.md, etc.).
var referencedPreviewablePathPattern = regexp.MustCompile(
	`(?i)` + referencedPathDelimiters + `([A-Za-z0-9._-]+(?:[\\/][A-Za-z0-9._-]+)*\.(?:` + previewableFileExtensions + `))`,
)

var referencedAbsolutePreviewablePathPattern = regexp.MustCompile(
	`(?i)` + referencedPathDelimiters + `((?:[A-Za-z]:)?(?:[\\/][A-Za-z0-9._-]+)+\.(?:` + previewableFileExtensions + `))`,
)

var referencedHomePreviewablePathPattern = regexp.MustCompile(
	`(?i)` + referencedPathDelimiters + `(~(?:[A-Za-z0-9._-]+(?:[\\/][A-Za-z0-9._-]+)*)\.(?:` + previewableFileExtensions + `))`,
)

const maxDeliverableHTMLBytes = 2 << 20
const maxDeliverableImageBytes = 5 << 20
const maxDeliverableTextBytes = 2 << 20

var imageExtensionMIME = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".svg":  "image/svg+xml",
}

var referencedImagePathPattern = regexp.MustCompile(`(?i)(?:attachments/)?[A-Za-z0-9._/-]+\.(?:png|jpe?g|gif|webp|bmp|svg)`)

// AttachmentBlocksFromDeliverableToolOutput extracts previewable file blocks from tool output.
func AttachmentBlocksFromDeliverableToolOutput(toolName, output, projectRoot string) []map[string]interface{} {
	if blocks := AttachmentBlocksFromWriteToolOutput(toolName, output, projectRoot); len(blocks) > 0 {
		return blocks
	}
	normalized := strings.ToLower(strings.TrimSpace(toolName))
	switch normalized {
	case "web_fetch":
		if path := extractLocalPathFromWebFetchOutput(output); path != "" {
			if blocks := attachmentBlocksFromLocalHTMLFile(projectRoot, path); len(blocks) > 0 {
				return blocks
			}
			return attachmentBlocksFromLocalImageFile(projectRoot, path)
		}
	case "glob", "grep", "bash", "read":
		return attachmentBlocksFromImagePathsInOutput(output, projectRoot)
	}
	return nil
}

// AttachmentBlocksFromWriteToolOutput reads previewable files written by filesystem write tools
// and returns chat content blocks the UI can preview and download.
func AttachmentBlocksFromWriteToolOutput(toolName, output, projectRoot string) []map[string]interface{} {
	normalized := strings.ToLower(strings.TrimSpace(toolName))
	switch normalized {
	case "write", "file_write", "write_file", "edit_file":
	default:
		return nil
	}
	rawPath := extractPathFromWriteToolOutput(output)
	if rawPath == "" {
		return nil
	}
	if blocks := attachmentBlocksFromLocalHTMLFile(projectRoot, rawPath); len(blocks) > 0 {
		return blocks
	}
	return attachmentBlocksFromLocalPreviewableFile(projectRoot, rawPath)
}

func extractPathFromWriteToolOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lower := strings.ToLower(output)
	if idx := strings.LastIndex(lower, " to "); idx >= 0 {
		return strings.Trim(strings.TrimSpace(output[idx+4:]), `"'`)
	}
	const updatedPrefix = "updated file "
	if strings.HasPrefix(lower, updatedPrefix) {
		return strings.TrimSpace(output[len(updatedPrefix):])
	}
	const replacedPrefix = "successfully replaced the string in "
	if strings.HasPrefix(lower, replacedPrefix) {
		path := strings.TrimSpace(output[len(replacedPrefix):])
		return strings.Trim(path, `"'`)
	}
	return ""
}

func extractLocalPathFromWebFetchOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Local file:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Local file:"))
		}
	}
	return ""
}

func attachmentBlocksFromLocalHTMLFile(projectRoot, rawPath string) []map[string]interface{} {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(rawPath))
	if ext != ".html" && ext != ".htm" {
		return nil
	}

	fullPath, err := resolveSandboxPath(projectRoot, rawPath)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(fullPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	if len(data) > maxDeliverableHTMLBytes {
		return nil
	}
	mime := "text/html; charset=utf-8"
	return []map[string]interface{}{{
		"type":      "file",
		"filename":  filepath.Base(rawPath),
		"mimeType":  mime,
		"sizeBytes": len(data),
		"source": map[string]interface{}{
			"type":       "base64",
			"media_type": mime,
			"data":       base64.StdEncoding.EncodeToString(data),
		},
	}}
}

func attachmentBlocksFromLocalImageFile(projectRoot, rawPath string) []map[string]interface{} {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(rawPath))
	mime, ok := imageExtensionMIME[ext]
	if !ok {
		return nil
	}
	fullPath, err := resolveSandboxPath(projectRoot, rawPath)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(fullPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	if len(data) > maxDeliverableImageBytes {
		return nil
	}
	return []map[string]interface{}{{
		"type":      "image",
		"filename":  filepath.Base(rawPath),
		"mimeType":  mime,
		"sizeBytes": len(data),
		"source": map[string]interface{}{
			"type":       "base64",
			"media_type": mime,
			"data":       base64.StdEncoding.EncodeToString(data),
		},
	}}
}

func previewableMIMEForExt(ext string) (string, bool) {
	switch strings.ToLower(ext) {
	case ".html", ".htm":
		return "text/html; charset=utf-8", true
	case ".json":
		return "application/json", true
	case ".txt", ".md", ".markdown", ".log", ".csv", ".xml", ".yaml", ".yml":
		return "text/plain; charset=utf-8", true
	case ".pdf":
		return "application/pdf", true
	default:
		return "", false
	}
}

func attachmentBlocksFromLocalPreviewableFile(projectRoot, rawPath string) []map[string]interface{} {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(rawPath))
	mime, ok := previewableMIMEForExt(ext)
	if !ok {
		return nil
	}
	fullPath, err := resolveSandboxPath(projectRoot, rawPath)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(fullPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	maxBytes := maxDeliverableTextBytes
	if ext == ".html" || ext == ".htm" {
		maxBytes = maxDeliverableHTMLBytes
	}
	if ext == ".pdf" {
		maxBytes = maxDeliverableHTMLBytes
	}
	if len(data) > maxBytes {
		return nil
	}
	blockType := "file"
	if _, isImage := imageExtensionMIME[ext]; isImage {
		blockType = "image"
	}
	return []map[string]interface{}{{
		"type":      blockType,
		"filename":  filepath.Base(rawPath),
		"mimeType":  mime,
		"sizeBytes": len(data),
		"source": map[string]interface{}{
			"type":       "base64",
			"media_type": mime,
			"data":       base64.StdEncoding.EncodeToString(data),
		},
	}}
}

func attachmentBlocksFromImagePathsInOutput(output, projectRoot string) []map[string]interface{} {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}
	seen := map[string]bool{}
	var blocks []map[string]interface{}
	addPath := func(rawPath string) {
		rawPath = strings.TrimSpace(rawPath)
		rawPath = strings.Trim(rawPath, `"'`)
		if rawPath == "" || seen[rawPath] {
			return
		}
		seen[rawPath] = true
		if chunk := attachmentBlocksFromLocalImageFile(projectRoot, rawPath); len(chunk) > 0 {
			blocks = append(blocks, chunk...)
		}
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isLikelyImagePath(line) {
			addPath(line)
		}
	}
	for _, match := range referencedImagePathPattern.FindAllString(output, -1) {
		addPath(match)
	}
	return blocks
}

func isLikelyImagePath(path string) bool {
	path = strings.TrimSpace(strings.Trim(path, `"'`))
	if path == "" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := imageExtensionMIME[ext]; !ok {
		return false
	}
	return !strings.Contains(path, "://")
}

func resolveSandboxPath(projectRoot, rawPath string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", fmt.Errorf("path is empty")
	}

	if strings.HasPrefix(strings.ToLower(rawPath), "file://") {
		cleaned := strings.TrimPrefix(rawPath, "file://")
		cleaned = strings.TrimPrefix(cleaned, "file:")
		if len(cleaned) >= 3 && cleaned[0] == '/' && cleaned[2] == ':' {
			cleaned = cleaned[1:]
		}
		rawPath = cleaned
	}

	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	fullPath := rawPath
	if !filepath.IsAbs(rawPath) {
		fullPath = filepath.Join(root, rawPath)
	}
	fullPath = filepath.Clean(fullPath)

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absFull, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}
	if absFull != absRoot && !strings.HasPrefix(absFull, absRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("path outside project root")
	}
	return absFull, nil
}

func looksLikeLocalResource(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	lower := strings.ToLower(rawURL)
	if strings.HasPrefix(lower, "file://") {
		return true
	}
	if strings.HasPrefix(rawURL, "./") || strings.HasPrefix(rawURL, "../") {
		return true
	}
	if strings.HasPrefix(rawURL, "/") {
		return true
	}
	if strings.HasPrefix(lower, "attachments/") || strings.HasPrefix(lower, "attachments\\") {
		return true
	}
	if len(rawURL) >= 2 && rawURL[1] == ':' {
		return true
	}
	u, err := urlParseNoScheme(rawURL)
	if err == nil && u.Scheme == "" && u.Host == "" && filepath.Ext(rawURL) != "" {
		return true
	}
	return false
}

type simpleURL struct {
	Scheme string
	Host   string
}

func urlParseNoScheme(raw string) (simpleURL, error) {
	if strings.Contains(raw, "://") {
		parts := strings.SplitN(raw, "://", 2)
		host := parts[1]
		if idx := strings.Index(host, "/"); idx >= 0 {
			host = host[:idx]
		}
		return simpleURL{Scheme: parts[0], Host: host}, nil
	}
	return simpleURL{}, nil
}

func readLocalFile(projectRoot, rawURL string) ([]byte, string, string, error) {
	fullPath, err := resolveSandboxPath(projectRoot, rawURL)
	if err != nil {
		return nil, "", "", err
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, "", "", err
	}
	ext := strings.ToLower(filepath.Ext(fullPath))
	contentType := "application/octet-stream"
	switch ext {
	case ".html", ".htm":
		contentType = "text/html; charset=utf-8"
	case ".json":
		contentType = "application/json"
	case ".txt", ".md", ".markdown", ".log", ".csv", ".xml", ".yaml", ".yml":
		contentType = "text/plain; charset=utf-8"
	}
	displayPath := fullPath
	if root := strings.TrimSpace(projectRoot); root != "" {
		if rel, err := filepath.Rel(root, fullPath); err == nil && rel != "" && !strings.HasPrefix(rel, "..") {
			displayPath = rel
		}
	}
	return data, contentType, displayPath, nil
}

func normalizeReferencedPath(raw string) string {
	return strings.TrimSpace(strings.Trim(raw, `"'`))
}

func referencedPreviewablePathPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		referencedPreviewablePathPattern,
		referencedAbsolutePreviewablePathPattern,
		referencedHomePreviewablePathPattern,
	}
}

func referencedPreviewablePathsFromText(text string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, pattern := range referencedPreviewablePathPatterns() {
		for _, groups := range pattern.FindAllStringSubmatch(text, -1) {
			if len(groups) < 2 {
				continue
			}
			path := normalizeReferencedPath(groups[1])
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths
}

func a2uiUpdateDataModelStringValue(raw interface{}) string {
	msg, ok := raw.(map[string]interface{})
	if !ok {
		return ""
	}
	udm, ok := msg["updateDataModel"].(map[string]interface{})
	if !ok {
		return ""
	}
	value, ok := udm["value"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func extractA2UITextFromContentBlock(block map[string]interface{}) string {
	a2uiRaw, ok := block["a2ui"]
	if !ok || a2uiRaw == nil {
		return ""
	}
	switch v := a2uiRaw.(type) {
	case map[string]interface{}:
		return a2uiUpdateDataModelStringValue(v)
	case []interface{}:
		var parts []string
		for _, item := range v {
			if text := a2uiUpdateDataModelStringValue(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func collectAssistantTextForAttachments(content []map[string]interface{}) string {
	var parts []string
	for _, block := range content {
		typ, _ := block["type"].(string)
		switch strings.ToLower(strings.TrimSpace(typ)) {
		case "text":
			if t, _ := block["text"].(string); strings.TrimSpace(t) != "" {
				parts = append(parts, t)
			}
		case "a2ui":
			if t := extractA2UITextFromContentBlock(block); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// AttachmentBlocksFromReferencedPaths loads HTML deliverables mentioned in assistant text.
func AttachmentBlocksFromReferencedPaths(text, projectRoot string) []map[string]interface{} {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	seen := map[string]bool{}
	var blocks []map[string]interface{}
	for _, match := range referencedAttachmentPathPattern.FindAllString(text, -1) {
		path := strings.TrimSpace(match)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if chunk := attachmentBlocksFromLocalHTMLFile(projectRoot, path); len(chunk) > 0 {
			blocks = append(blocks, chunk...)
		}
	}
	for _, path := range referencedPreviewablePathsFromText(text) {
		if seen[path] {
			continue
		}
		seen[path] = true
		if chunk := attachmentBlocksFromLocalPreviewableFile(projectRoot, path); len(chunk) > 0 {
			blocks = append(blocks, chunk...)
		}
	}
	for _, match := range referencedImagePathPattern.FindAllString(text, -1) {
		path := strings.TrimSpace(match)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if chunk := attachmentBlocksFromLocalImageFile(projectRoot, path); len(chunk) > 0 {
			blocks = append(blocks, chunk...)
		}
	}
	return blocks
}

// MergeDeliverableAttachmentBlocks appends HTML file blocks referenced in assistant text.
func MergeDeliverableAttachmentBlocks(content []map[string]interface{}, projectRoot string) []map[string]interface{} {
	if len(content) == 0 {
		return content
	}
	existing := map[string]bool{}
	for _, block := range content {
		if fn, _ := block["filename"].(string); fn != "" {
			existing[strings.ToLower(fn)] = true
		}
	}
	textPartsStr := collectAssistantTextForAttachments(content)
	for _, block := range AttachmentBlocksFromReferencedPaths(textPartsStr, projectRoot) {
		fn, _ := block["filename"].(string)
		if fn != "" && existing[strings.ToLower(fn)] {
			continue
		}
		content = append(content, block)
		if fn != "" {
			existing[strings.ToLower(fn)] = true
		}
	}
	return content
}

// AttachmentBlockFromLocalPath returns a previewable file block for a sandbox-relative path.
func AttachmentBlockFromLocalPath(projectRoot, rawPath string) (map[string]interface{}, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return nil, fmt.Errorf("path is required")
	}
	ext := strings.ToLower(filepath.Ext(rawPath))
	if ext == ".html" || ext == ".htm" {
		blocks := attachmentBlocksFromLocalHTMLFile(projectRoot, rawPath)
		if len(blocks) == 0 {
			return nil, fmt.Errorf("attachment not found or unreadable")
		}
		return blocks[0], nil
	}
	if blocks := attachmentBlocksFromLocalImageFile(projectRoot, rawPath); len(blocks) > 0 {
		return blocks[0], nil
	}
	if blocks := attachmentBlocksFromLocalPreviewableFile(projectRoot, rawPath); len(blocks) > 0 {
		return blocks[0], nil
	}
	return nil, fmt.Errorf("attachment not found or unreadable")
}
