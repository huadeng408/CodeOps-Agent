package permission

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

type AllowRule struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
}

func (r AllowRule) Matches(tool string, params map[string]any) bool {
	if r.Tool != "" && r.Tool != tool {
		return false
	}
	if r.Pattern == "" {
		return true
	}

	payload := tool
	if len(params) > 0 {
		if data, err := json.Marshal(params); err == nil {
			payload = payload + " " + string(data)
		}
	}

	if ok, _ := regexp.MatchString(r.Pattern, payload); ok {
		return true
	}
	return strings.Contains(payload, r.Pattern)
}

// Suggestion 描述一条由历史审批记录推导出的候选允许规则。
type Suggestion struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
	Count   int    `json:"count"`
	Sample  string `json:"sample,omitempty"`
}

// bashPrefixTokens 是归纳 Bash 命令签名时保留的前缀 token 数（例如
// "go test ./..." -> "go test"）。
const bashPrefixTokens = 2

// Suggester 扫描近期 ApprovalRecord，对重复出现的可泛化签名提出候选 AllowRule。
type Suggester struct {
	// MinCount 是同一 (工具, 签名) 至少需要出现的次数才会被建议为规则。
	// 未设置（<=0）时使用默认值 3。
	MinCount int
}

// DefaultSuggester 是 Controller.SuggestRules 使用的默认建议器。
var DefaultSuggester = &Suggester{MinCount: 3}

// SuggestRules 是对默认 Suggester 的便捷包装。
func SuggestRules(records []ApprovalRecord) []Suggestion {
	return DefaultSuggester.Suggest(records)
}

// Suggest 基于历史记录返回候选规则，按 Count 降序、再按 (工具, 模式) 升序排列。
// 它只读取 records，不会修改任何状态。
func (s *Suggester) Suggest(records []ApprovalRecord) []Suggestion {
	minCount := s.MinCount
	if minCount <= 0 {
		minCount = 3
	}

	type group struct {
		tool      string
		signature string
		count     int
		sample    string
	}
	groups := map[string]*group{}
	order := []string{}

	for _, rec := range records {
		sig := strings.TrimSpace(rec.Signature)
		if sig == "" {
			continue
		}
		key := rec.Tool + "\x00" + sig
		g, ok := groups[key]
		if !ok {
			g = &group{tool: rec.Tool, signature: sig, sample: rec.Sample}
			groups[key] = g
			order = append(order, key)
		}
		g.count++
		if rec.Sample != "" {
			g.sample = rec.Sample
		}
	}

	out := make([]Suggestion, 0)
	for _, key := range order {
		g := groups[key]
		if g.count < minCount {
			continue
		}
		pattern := suggestPattern(g.tool, g.signature)
		if pattern == "" {
			continue
		}
		out = append(out, Suggestion{
			Tool:    g.tool,
			Pattern: pattern,
			Count:   g.count,
			Sample:  g.sample,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tool+"\x00"+out[i].Pattern < out[j].Tool+"\x00"+out[j].Pattern
	})
	return out
}

// suggestPattern 把一个归纳出的签名转换成可用于 AllowRule.Pattern 的匹配模式。
func suggestPattern(tool, signature string) string {
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return ""
	}
	if isFileTool(tool) && !strings.HasSuffix(signature, "/") {
		return signature + "/"
	}
	return signature
}

// GeneralizeSignature 从工具参数中抽取一个可泛化的签名以及用于展示的原始样本。
// 例如 Bash "go test ./..." -> ("go test", "go test ./...")；
// Write "/repo/docs/a.md" -> ("/repo/docs", "/repo/docs/a.md")。
// 无法归纳时返回空签名。
func GeneralizeSignature(tool string, params map[string]any) (signature, sample string) {
	tool = strings.TrimSpace(tool)
	switch tool {
	case "Bash":
		cmd, _ := params["command"].(string)
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			return "", ""
		}
		tokens := strings.Fields(cmd)
		if len(tokens) == 0 {
			return "", ""
		}
		if len(tokens) > bashPrefixTokens {
			tokens = tokens[:bashPrefixTokens]
		}
		return strings.Join(tokens, " "), cmd
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		path := fileParam(params)
		path = strings.TrimSpace(path)
		if path == "" {
			return "", ""
		}
		dir := pathDir(path)
		if dir == "" {
			return "", path
		}
		return dir, path
	default:
		return "", ""
	}
}

func isFileTool(tool string) bool {
	switch tool {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return true
	}
	return false
}

func fileParam(params map[string]any) string {
	if params == nil {
		return ""
	}
	for _, key := range []string{"path", "file_path", "filePath", "notebook_path"} {
		if v, ok := params[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// pathDir 返回路径所在的目录（统一使用正斜杠，便于跨平台生成稳定的签名）。
// 没有目录部分时返回空字符串。
func pathDir(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimRight(p, "/")
	idx := strings.LastIndex(p, "/")
	if idx <= 0 {
		return ""
	}
	return p[:idx]
}
