package pipeline

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"code-agent/internal/model"
	orchestratorclient "code-agent/pkg/orchestrator"
	"code-agent/pkg/tasks"
)

type parsedElementProvenance struct {
	DocumentID    string `json:"document_id"`
	ElementID     string `json:"element_id"`
	ParserName    string `json:"parser_name"`
	ParserVersion string `json:"parser_version"`
	SourceSHA256  string `json:"source_sha256"`
}

type verifiedStructuredArtifact struct {
	documentID    string
	parserName    string
	parserVersion string
	payloadSHA256 string
	elementIDs    map[string]struct{}
	mineru        bool
}

func validateParsedArtifactProvenance(task tasks.FileProcessingTask, artifact orchestratorclient.ParsedArtifact) (*verifiedStructuredArtifact, error) {
	taskDocumentID := taskDocumentID(task)
	hasElements := len(artifact.Elements) > 0
	requireMinerU := strings.EqualFold(filepath.Ext(task.FileName), ".pdf") || claimsMinerUIdentity(artifact.ParserName)
	elements := make([]parsedElementProvenance, 0, len(artifact.Elements))
	for index, raw := range artifact.Elements {
		var element parsedElementProvenance
		if err := json.Unmarshal(raw, &element); err != nil {
			return nil, fmt.Errorf("structured element %d provenance is invalid: %w", index, err)
		}
		elements = append(elements, element)
		if claimsMinerUIdentity(element.ParserName) {
			requireMinerU = true
		}
	}
	if !hasElements {
		if strings.EqualFold(filepath.Ext(task.FileName), ".pdf") ||
			strings.TrimSpace(artifact.DocumentID) != "" ||
			strings.TrimSpace(artifact.ParserName) != "" ||
			strings.TrimSpace(artifact.ParserVersion) != "" {
			return nil, fmt.Errorf("structured artifacts require element provenance")
		}
		return nil, nil
	}
	if hasElements {
		// Every structured response must stay bound to the file task. Without
		// this check a native (for example openpyxl) artifact could be persisted
		// under a different document identity than the object that produced it.
		if strings.TrimSpace(artifact.DocumentID) == "" || artifact.DocumentID != taskDocumentID {
			return nil, fmt.Errorf("structured document provenance does not match task document identity")
		}
		if strings.TrimSpace(artifact.ParserName) == "" {
			return nil, fmt.Errorf("structured parser name is required")
		}
		if strings.TrimSpace(artifact.ParserVersion) == "" {
			return nil, fmt.Errorf("structured parser version is required")
		}
		if !isLowerSHA256(artifact.SourceSHA256) {
			return nil, fmt.Errorf("structured source_sha256 must be a lowercase SHA-256")
		}
		if task.Provenance != nil && strings.TrimSpace(task.Provenance.SourceSHA256) != "" && artifact.SourceSHA256 != task.Provenance.SourceSHA256 {
			return nil, fmt.Errorf("structured raw source_sha256 does not match task provenance")
		}
	}
	payloadSHA256 := ""
	elementIDs := make(map[string]struct{}, len(elements))
	for index, element := range elements {
		if strings.TrimSpace(element.DocumentID) == "" || element.DocumentID != artifact.DocumentID {
			return nil, fmt.Errorf("structured element %d document provenance does not match artifact", index)
		}
		if strings.TrimSpace(element.ParserName) == "" {
			return nil, fmt.Errorf("structured element %d parser name is required", index)
		}
		if strings.TrimSpace(element.ElementID) == "" {
			return nil, fmt.Errorf("structured element %d element_id is required", index)
		}
		elementIDs[element.ElementID] = struct{}{}
		if !strings.EqualFold(strings.TrimSpace(element.ParserName), strings.TrimSpace(artifact.ParserName)) {
			if claimsMinerUIdentity(element.ParserName) || claimsMinerUIdentity(artifact.ParserName) {
				return nil, fmt.Errorf("MinerU parser identity must be exact")
			}
			return nil, fmt.Errorf("structured element %d parser provenance does not match artifact", index)
		}
		if strings.TrimSpace(element.ParserVersion) == "" || element.ParserVersion != artifact.ParserVersion {
			return nil, fmt.Errorf("structured element %d parser version does not match artifact", index)
		}
		if !isLowerSHA256(element.SourceSHA256) {
			return nil, fmt.Errorf("structured element %d source_sha256 must be a lowercase SHA-256", index)
		}
		if payloadSHA256 == "" {
			payloadSHA256 = element.SourceSHA256
		} else if element.SourceSHA256 != payloadSHA256 {
			return nil, fmt.Errorf("structured element %d payload hash does not match prior elements", index)
		}
	}
	if !requireMinerU {
		if payloadSHA256 != artifact.SourceSHA256 {
			return nil, fmt.Errorf("structured element payload hash does not match artifact source hash")
		}
		return &verifiedStructuredArtifact{
			documentID:    artifact.DocumentID,
			parserName:    artifact.ParserName,
			parserVersion: artifact.ParserVersion,
			payloadSHA256: payloadSHA256,
			elementIDs:    elementIDs,
		}, nil
	}
	if strings.TrimSpace(strings.ToLower(artifact.ParserName)) != "mineru" {
		return nil, fmt.Errorf("MinerU parser identity must be exact")
	}
	if strings.TrimSpace(artifact.DocumentID) == "" || artifact.DocumentID != taskDocumentID {
		return nil, fmt.Errorf("MinerU document provenance does not match task document identity")
	}
	if strings.TrimSpace(artifact.ParserVersion) == "" {
		return nil, fmt.Errorf("MinerU parser version is required")
	}
	if !isLowerSHA256(artifact.SourceSHA256) {
		return nil, fmt.Errorf("MinerU raw source_sha256 must be a lowercase SHA-256")
	}
	if len(elements) == 0 {
		return nil, fmt.Errorf("MinerU elements are required")
	}
	for index, element := range elements {
		if element.DocumentID != artifact.DocumentID {
			return nil, fmt.Errorf("MinerU element %d document provenance does not match artifact", index)
		}
		if strings.TrimSpace(strings.ToLower(element.ParserName)) != "mineru" {
			return nil, fmt.Errorf("MinerU element %d parser identity must be exact", index)
		}
		if element.ParserVersion != artifact.ParserVersion {
			return nil, fmt.Errorf("MinerU element %d parser version does not match artifact", index)
		}
		if !isLowerSHA256(element.SourceSHA256) {
			return nil, fmt.Errorf("MinerU element %d source_sha256 must be a lowercase SHA-256", index)
		}
		if payloadSHA256 == "" {
			payloadSHA256 = element.SourceSHA256
		} else if element.SourceSHA256 != payloadSHA256 {
			return nil, fmt.Errorf("MinerU element %d payload hash does not match prior elements", index)
		}
	}
	return &verifiedStructuredArtifact{
		documentID:    artifact.DocumentID,
		parserName:    artifact.ParserName,
		parserVersion: artifact.ParserVersion,
		payloadSHA256: payloadSHA256,
		elementIDs:    elementIDs,
		mineru:        true,
	}, nil
}

func taskDocumentID(task tasks.FileProcessingTask) string {
	if documentID := strings.TrimSpace(task.DocumentID); documentID != "" {
		return documentID
	}
	return task.FileMD5
}

func validateStructuredChunkBinding(verified *verifiedStructuredArtifact, chunk model.StructuredChunk) error {
	if verified == nil {
		return nil
	}
	if chunk.DocumentID != verified.documentID {
		return fmt.Errorf("document provenance does not match parsed artifact")
	}
	if !strings.EqualFold(strings.TrimSpace(chunk.ParserName), strings.TrimSpace(verified.parserName)) {
		return fmt.Errorf("parser provenance does not match parsed artifact")
	}
	if chunk.ParserVersion != verified.parserVersion {
		return fmt.Errorf("parser version does not match parsed artifact")
	}
	if chunk.SourceSHA256 != verified.payloadSHA256 {
		return fmt.Errorf("payload source_sha256 does not match parsed elements")
	}
	for _, elementID := range chunk.ElementIDs {
		if _, ok := verified.elementIDs[elementID]; !ok {
			return fmt.Errorf("element provenance does not match parsed artifact")
		}
	}
	if verified.mineru && strings.TrimSpace(strings.ToLower(chunk.ParserName)) != "mineru" {
		return fmt.Errorf("parser provenance must use exact MinerU identity")
	}
	return nil
}

func claimsMinerUIdentity(parserName string) bool {
	normalized := strings.TrimSpace(strings.ToLower(parserName))
	return normalized == "mineru" || strings.HasPrefix(normalized, "mineru-")
}

func isLowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
