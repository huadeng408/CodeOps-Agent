package admission

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"
)

type streamProtocolError string

func (failure streamProtocolError) Error() string { return string(failure) }

type streamProviderError struct{ metadata json.RawMessage }

func (*streamProviderError) Error() string { return "provider stream error" }

// Streams stay inside Go until completion, identity and final usage can be checked.
// shortcut: text/reasoning and function deltas only; extend when streamed media output is admitted.
func readOpenAIStream(response *http.Response, requestedModel string) ([]byte, error) {
	kind, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || kind != "text/event-stream" {
		return nil, streamProtocolError("unexpected model stream content type")
	}
	const payloadLimit = 16 << 20
	// Wire framing can be much larger than the assembled completion.
	limited := &io.LimitedReader{R: response.Body, N: (128 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 64<<10), payloadLimit+1)
	var id, model, fingerprint, finish string
	var created int64
	stringsOut := map[string]*strings.Builder{}
	type toolParts struct {
		id, kind   string
		name, args strings.Builder
	}
	tools := map[int]*toolParts{}
	var usage json.RawMessage
	var lastUsage tokenUsage
	sawUsage := false
	assembledBytes := 0
	appendText := func(target *strings.Builder, text string) error {
		assembledBytes += len(text)
		if assembledBytes > payloadLimit {
			return streamProtocolError("model completion too large")
		}
		target.WriteString(text)
		return nil
	}
	consume := func(data string) (bool, error) {
		if strings.TrimSpace(data) == "" {
			return false, nil
		}
		if data == "[DONE]" {
			return true, nil
		}
		fields, err := streamJSONFields([]byte(data), "id", "model", "system_fingerprint", "created", "choices", "usage", "error")
		if err != nil {
			if !json.Valid([]byte(data)) {
				return false, streamProtocolError("invalid model stream JSON")
			}
			return false, err
		}
		if fields["error"] != nil && !bytes.Equal(bytes.TrimSpace(fields["error"]), []byte("null")) {
			return false, &streamProviderError{metadata: fields["error"]}
		}
		var chunk struct {
			ID, Model   string
			Fingerprint string `json:"system_fingerprint"`
			Created     int64
			Choices     []json.RawMessage
			Usage       json.RawMessage
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			return false, streamProtocolError("unsupported model stream field types")
		}
		for _, pair := range []struct {
			target *string
			value  string
			limit  int
		}{{&id, chunk.ID, 1024}, {&model, chunk.Model, 256}, {&fingerprint, chunk.Fingerprint, 256}} {
			if len(pair.value) > pair.limit || pair.value != "" && *pair.target != "" && pair.value != *pair.target {
				return false, streamProtocolError("inconsistent model stream identity")
			}
			if pair.value != "" {
				*pair.target = pair.value
			}
		}
		if chunk.Created < 0 || created != 0 && chunk.Created != 0 && created != chunk.Created {
			return false, streamProtocolError("inconsistent model stream identity")
		}
		if chunk.Created != 0 {
			created = chunk.Created
		}
		if len(chunk.Choices) > 1 {
			return false, streamProtocolError("multiple stream choices are not admitted")
		}
		for _, rawChoice := range chunk.Choices {
			fields, err := streamJSONFields(rawChoice, "index", "delta", "finish_reason")
			if err != nil {
				return false, err
			}
			if raw := fields["delta"]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				if _, err := streamJSONFields(raw, "role", "content", "reasoning_content", "reasoning", "refusal", "tool_calls"); err != nil {
					return false, err
				}
			}
			var choice struct {
				Index  *int
				Delta  map[string]json.RawMessage
				Finish *string `json:"finish_reason"`
			}
			if json.Unmarshal(rawChoice, &choice) != nil {
				return false, streamProtocolError("invalid model stream choice")
			}
			if choice.Index == nil || *choice.Index != 0 || finish != "" && len(choice.Delta) != 0 {
				return false, streamProtocolError("invalid model stream choice")
			}
			for _, key := range []string{"role", "content", "reasoning_content", "reasoning", "refusal"} {
				raw, exists := choice.Delta[key]
				if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					continue
				}
				var text string
				if json.Unmarshal(raw, &text) != nil || key == "role" && text != "assistant" {
					return false, streamProtocolError("invalid model stream text")
				}
				if key == "role" {
					continue
				}
				if stringsOut[key] == nil {
					stringsOut[key] = &strings.Builder{}
				}
				if err := appendText(stringsOut[key], text); err != nil {
					return false, err
				}
			}
			var deltas []json.RawMessage
			if raw := choice.Delta["tool_calls"]; raw != nil && json.Unmarshal(raw, &deltas) != nil {
				return false, streamProtocolError("invalid model stream tools")
			}
			for _, rawDelta := range deltas {
				fields, err := streamJSONFields(rawDelta, "index", "id", "type", "function")
				if err != nil {
					return false, err
				}
				if raw := fields["function"]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					if _, err := streamJSONFields(raw, "name", "arguments"); err != nil {
						return false, err
					}
				}
				var delta struct {
					Index    *int
					ID, Type string
					Function struct{ Name, Arguments string }
				}
				if json.Unmarshal(rawDelta, &delta) != nil {
					return false, streamProtocolError("invalid model stream tool")
				}
				if delta.Index == nil || *delta.Index < 0 || *delta.Index >= 128 || len(delta.ID) > 256 || delta.Type != "" && delta.Type != "function" {
					return false, streamProtocolError("invalid model stream tool")
				}
				index := *delta.Index
				if tools[index] == nil {
					tools[index] = &toolParts{}
				}
				slot := tools[index]
				if delta.ID != "" && slot.id != "" && delta.ID != slot.id {
					return false, streamProtocolError("inconsistent model stream tool")
				}
				if delta.ID != "" {
					slot.id = delta.ID
				}
				if delta.Type != "" {
					slot.kind = delta.Type
				}
				if err := appendText(&slot.name, delta.Function.Name); err != nil {
					return false, err
				}
				if err := appendText(&slot.args, delta.Function.Arguments); err != nil {
					return false, err
				}
			}
			if choice.Finish != nil {
				if *choice.Finish == "" || finish != "" && finish != *choice.Finish {
					return false, streamProtocolError("inconsistent model stream finish")
				}
				finish = *choice.Finish
			}
		}
		if raw := bytes.TrimSpace(chunk.Usage); len(raw) != 0 && !bytes.Equal(raw, []byte("null")) {
			current, err := normalizeUsage("openai", requestedModel, append(append([]byte(`{"usage":`), raw...), '}'))
			if err != nil || sawUsage && (current.input < lastUsage.input || current.output < lastUsage.output || current.cached < lastUsage.cached) {
				return false, streamProtocolError("inconsistent model stream usage")
			}
			lastUsage, sawUsage = current, true
			if finish != "" {
				usage = raw
			}
		}
		return false, nil
	}
	var event []string
	eventBytes, done, first := 0, false, true
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		if line == "" && len(event) > 0 {
			done, err = consume(strings.Join(event, "\n"))
			event, eventBytes = nil, 0
			if err != nil || done {
				break
			}
		} else if strings.HasPrefix(line, "data:") {
			part := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			eventBytes += len(part) + 1
			if eventBytes > payloadLimit {
				return nil, streamProtocolError("model stream event too large")
			}
			event = append(event, part)
		}
	}
	if err == nil && !done && len(event) > 0 {
		done, err = consume(strings.Join(event, "\n"))
	}
	if err != nil {
		return nil, err
	}
	if scanner.Err() != nil {
		return nil, scanner.Err()
	}
	if !done || limited.N == 0 || model == "" || finish == "" {
		return nil, io.ErrUnexpectedEOF
	}
	message := map[string]any{"role": "assistant", "content": ""}
	for key, text := range stringsOut {
		message[key] = text.String()
	}
	indices := make([]int, 0, len(tools))
	for index := range tools {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	var calls []any
	for position, index := range indices {
		slot := tools[index]
		if index != position || slot.id == "" || slot.kind != "function" || slot.name.Len() == 0 {
			return nil, streamProtocolError("incomplete model stream tool")
		}
		calls = append(calls, map[string]any{"id": slot.id, "type": slot.kind, "function": map[string]any{"name": slot.name.String(), "arguments": slot.args.String()}})
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	return json.Marshal(map[string]any{"id": id, "model": model, "system_fingerprint": fingerprint, "created": created, "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": usage})
}

func streamJSONFields(data []byte, names ...string) (map[string]json.RawMessage, error) {
	fields, err := accountingJSONFields(data, names...)
	if err != nil {
		return nil, err
	}
	// JSON keys are case-sensitive; Go's case folding must not change wire accounting or tools.
	for key := range fields {
		for _, name := range names {
			if key != name && strings.EqualFold(key, name) {
				return nil, streamProtocolError("ambiguous model stream field")
			}
		}
	}
	return fields, nil
}
