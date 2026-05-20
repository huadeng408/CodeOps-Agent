package permission

import (
	"encoding/json"
	"regexp"
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
