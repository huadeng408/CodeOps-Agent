package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// notebookDoc 表示 .ipynb 顶层结构。metadata / nbformat / nbformat_minor 用
// json.RawMessage 保留原始内容，避免回写时改变既有字段或键顺序。
type notebookDoc struct {
	Cells         []notebookCell  `json:"cells"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	NBFormat      json.RawMessage `json:"nbformat,omitempty"`
	NBFormatMinor json.RawMessage `json:"nbformat_minor,omitempty"`
}

// notebookCell 表示单个 notebook 单元格。Source/Outputs/ExecutionCount 等
// 字段使用 json.RawMessage 以便忠实保留未修改单元格的原始 JSON 形态。
type notebookCell struct {
	CellType       string          `json:"cell_type"`
	ID             string          `json:"id,omitempty"`
	Source         json.RawMessage `json:"source,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Outputs        json.RawMessage `json:"outputs,omitempty"`
	ExecutionCount json.RawMessage `json:"execution_count,omitempty"`
	Attachments    json.RawMessage `json:"attachments,omitempty"`
}

// executeNotebookEdit 对 .ipynb 文件做单元格级别的编辑，支持 insert/replace/delete
// 三种模式。单元格可通过 cell_id 定位，未提供时回退到 cell_index（数字）。该方法
// 复用 secureFilePath 做工作区校验，并通过原子写入和统一结果处理器
// 保证 symlink 安全及有界输出。
func (e *Executor) executeNotebookEdit(_ context.Context, args map[string]any) (ToolResult, error) {
	path, ok := stringArg(args, "path", "file")
	if !ok || path == "" {
		return ToolResult{Name: "NotebookEdit", Error: "path is required"}, fmt.Errorf("path is required")
	}
	abs, err := secureFilePath(e.Root, path)
	if err != nil {
		return ToolResult{Name: "NotebookEdit", Error: err.Error()}, err
	}

	editMode, _ := stringArg(args, "edit_mode", "mode")
	editMode = strings.ToLower(strings.TrimSpace(editMode))
	if editMode == "" {
		editMode = "replace"
	}
	switch editMode {
	case "insert", "replace", "delete":
	default:
		err := fmt.Errorf("invalid edit_mode %q (want insert, replace, or delete)", editMode)
		return ToolResult{Name: "NotebookEdit", Error: err.Error(), ExitCode: 1}, err
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return ToolResult{Name: "NotebookEdit", Error: err.Error()}, err
	}
	var doc notebookDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		err := fmt.Errorf("invalid notebook JSON in %s: %w", path, err)
		return ToolResult{Name: "NotebookEdit", Error: err.Error(), ExitCode: 1}, err
	}
	if doc.Cells == nil {
		err := fmt.Errorf("notebook %s has no cells array", path)
		return ToolResult{Name: "NotebookEdit", Error: err.Error(), ExitCode: 1}, err
	}

	cellID, hasID := stringArg(args, "cell_id", "id")
	cellID = strings.TrimSpace(cellID)
	index, hasIndex, err := intArg(args, "cell_index", "index")
	if err != nil {
		return ToolResult{Name: "NotebookEdit", Error: err.Error()}, err
	}

	before := string(data)
	var summary string
	switch editMode {
	case "delete":
		target, err := locateCell(doc.Cells, cellID, hasID, index, hasIndex)
		if err != nil {
			return ToolResult{Name: "NotebookEdit", Error: err.Error(), ExitCode: 1}, err
		}
		doc.Cells = append(doc.Cells[:target], doc.Cells[target+1:]...)
		summary = fmt.Sprintf("deleted cell %d from %s", target, filepath.ToSlash(path))
	case "replace":
		cellType, _ := stringArg(args, "cell_type", "type")
		source, _ := stringArg(args, "source", "content")
		cellType = strings.ToLower(strings.TrimSpace(cellType))
		if cellType == "" {
			cellType = "code"
		}
		if err := validateCellType(cellType); err != nil {
			return ToolResult{Name: "NotebookEdit", Error: err.Error(), ExitCode: 1}, err
		}
		target, err := locateCell(doc.Cells, cellID, hasID, index, hasIndex)
		if err != nil {
			return ToolResult{Name: "NotebookEdit", Error: err.Error(), ExitCode: 1}, err
		}
		doc.Cells[target] = mergeCell(doc.Cells[target], cellType, source)
		summary = fmt.Sprintf("replaced cell %d in %s", target, filepath.ToSlash(path))
	case "insert":
		cellType, _ := stringArg(args, "cell_type", "type")
		source, _ := stringArg(args, "source", "content")
		cellType = strings.ToLower(strings.TrimSpace(cellType))
		if cellType == "" {
			cellType = "code"
		}
		if err := validateCellType(cellType); err != nil {
			return ToolResult{Name: "NotebookEdit", Error: err.Error(), ExitCode: 1}, err
		}
		newCell := notebookCell{
			CellType: cellType,
			ID:       newCellID(doc.Cells),
			Source:   sourceToRawList(source),
		}
		insertAt := len(doc.Cells)
		if hasID && cellID != "" || hasIndex {
			target, err := locateCell(doc.Cells, cellID, hasID, index, hasIndex)
			if err != nil {
				return ToolResult{Name: "NotebookEdit", Error: err.Error(), ExitCode: 1}, err
			}
			insertAt = target + 1
		}
		doc.Cells = append(doc.Cells[:insertAt], append([]notebookCell{newCell}, doc.Cells[insertAt:]...)...)
		summary = fmt.Sprintf("inserted %s cell at position %d in %s", cellType, insertAt, filepath.ToSlash(path))
	}

	encoded, err := encodeNotebook(&doc)
	if err != nil {
		return ToolResult{Name: "NotebookEdit", Error: err.Error()}, err
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(abs); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := atomicWriteFile(abs, encoded, mode); err != nil {
		return ToolResult{Name: "NotebookEdit", Error: err.Error()}, err
	}
	return ToolResult{
		Name:   "NotebookEdit",
		Output: summary,
		Changes: []Change{{
			Path:   filepath.ToSlash(path),
			Before: before,
			After:  string(encoded),
		}},
	}, nil
}

// locateCell 把 (cell_id | cell_index) 解析成单元格在切片中的下标。
func locateCell(cells []notebookCell, cellID string, hasID bool, index int, hasIndex bool) (int, error) {
	if hasID && cellID != "" {
		for i, c := range cells {
			if c.ID == cellID {
				return i, nil
			}
		}
		return 0, fmt.Errorf("cell_id %q not found", cellID)
	}
	if hasIndex {
		if index < 0 || index >= len(cells) {
			return 0, fmt.Errorf("cell_index %d out of range (0-%d)", index, len(cells)-1)
		}
		return index, nil
	}
	return 0, fmt.Errorf("cell_id or cell_index is required")
}

// mergeCell 用新的 cell_type/source 更新既有单元格：保留 id 与 metadata，按类型
// 重置 outputs / execution_count（markdown/raw 不携带这些字段）。
func mergeCell(old notebookCell, cellType, source string) notebookCell {
	updated := notebookCell{
		CellType: cellType,
		ID:       old.ID,
		Source:   sourceToRawList(source),
		Metadata: old.Metadata,
	}
	if cellType == "code" {
		updated.Outputs = old.Outputs
		updated.ExecutionCount = old.ExecutionCount
	}
	if old.Attachments != nil {
		updated.Attachments = old.Attachments
	}
	return updated
}

func validateCellType(cellType string) error {
	switch cellType {
	case "code", "markdown", "raw":
		return nil
	default:
		return fmt.Errorf("invalid cell_type %q (want code, markdown, or raw)", cellType)
	}
}

// newCellID 生成一个在当前 notebook 内唯一的单元格 id（8 字节 hex）。
func newCellID(cells []notebookCell) string {
	existing := make(map[string]struct{}, len(cells))
	for _, c := range cells {
		if c.ID != "" {
			existing[c.ID] = struct{}{}
		}
	}
	for i := 0; i < 8; i++ {
		var buf [8]byte
		if _, err := rand.Read(buf[:]); err != nil {
			// 极端情况下回退到基于数量的 id，保证可读且非空。
			return fmt.Sprintf("cell-%d", len(cells))
		}
		id := "c" + hex.EncodeToString(buf[:])
		if _, ok := existing[id]; !ok {
			return id
		}
	}
	return fmt.Sprintf("cell-%d", len(cells))
}

// sourceToRawList 将任意源文本编码为 ipynb 规范的字符串数组形式（每行保留尾随
// 换行，最后一行不带换行）。空字符串返回 [""] 的等价 JSON 以保持字段非空。
func sourceToRawList(source string) json.RawMessage {
	text := strings.ReplaceAll(source, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines)-1; i++ {
		lines[i] = lines[i] + "\n"
	}
	encoded, err := json.Marshal(lines)
	if err != nil {
		// 兜底：原样作为单字符串。
		encoded, _ = json.Marshal([]string{text})
	}
	return encoded
}

// sourceToText 把单元格 source 字段（可能是 string 或 []string）还原为纯文本。
func sourceToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// 先尝试按字符串数组解析（ipynb 规范形态）。
	var lines []string
	if err := json.Unmarshal(raw, &lines); err == nil {
		return strings.Join(lines, "")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return ""
}

// encodeNotebook 以 ipynb 约定的格式（1 空格缩进、不转义 HTML、末尾换行）回写。
func encodeNotebook(doc *notebookDoc) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("failed to encode notebook: %w", err)
	}
	return buf.Bytes(), nil
}

// renderNotebookSummary 把 .ipynb 渲染成简洁可读的单元格摘要，供 Read 工具返回。
func renderNotebookSummary(path string, data []byte) string {
	var doc notebookDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Sprintf("[Notebook %s: invalid JSON: %v]", path, err)
	}
	nbformat := rawInt(doc.NBFormat)
	nbminor := rawInt(doc.NBFormatMinor)
	var b strings.Builder
	if nbformat > 0 {
		fmt.Fprintf(&b, "[Notebook %s: nbformat %d.%d, %d cell(s)]", path, nbformat, nbminor, len(doc.Cells))
	} else {
		fmt.Fprintf(&b, "[Notebook %s: %d cell(s)]", path, len(doc.Cells))
	}
	for i, cell := range doc.Cells {
		cellType := cell.CellType
		if cellType == "" {
			cellType = "code"
		}
		fmt.Fprintf(&b, "\n\nCell %d (%s)", i, cellType)
		if cell.ID != "" {
			fmt.Fprintf(&b, " id=%s", cell.ID)
		}
		text := strings.TrimRight(sourceToText(cell.Source), "\n")
		if text != "" {
			b.WriteString("\n")
			b.WriteString(indentBlock(text, "  "))
		}
	}
	return b.String()
}

// indentBlock 给每行增加前缀，便于在摘要中以缩进区分单元格源码。
func indentBlock(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// rawInt 从 json.RawMessage 中尽量解析出一个整数（用于 nbformat 显示）。
func rawInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return int(f)
	}
	return 0
}
