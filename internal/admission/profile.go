package admission

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"

	"code-agent/internal/session"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (config ProviderConfig) ValidateResponseProfile() error {
	if config.ResponseMode != "" && (config.ResponseMode != "sse" || config.Protocol != "openai") {
		return ErrInvalidAdmission
	}
	return nil
}

func (config ProviderConfig) ValidateReasoningProfile() error {
	if config.ReasoningEffort == "" {
		return nil
	}
	if config.Protocol != "openai" {
		return ErrInvalidAdmission
	}
	if validReasoningEffort(config.ReasoningEffort) {
		return nil
	}
	return ErrInvalidAdmission
}

func validReasoningEffort(value string) bool {
	switch value {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}

func (config ProviderConfig) applyRequestProfile(payload map[string]any) error {
	if config.ReasoningEffort != "" {
		payload["reasoning_effort"] = config.ReasoningEffort
	}
	if config.Protocol != "anthropic" {
		return nil
	}
	raw, exists := payload["thinking"]
	if !exists {
		return nil
	}
	thinking, ok := raw.(map[string]any)
	if !ok {
		return status.Error(codes.InvalidArgument, "invalid thinking configuration")
	}
	if thinking["type"] != "enabled" {
		return nil
	}
	budget, ok := thinking["budget_tokens"].(float64)
	if !ok || math.IsNaN(budget) || math.IsInf(budget, 0) || math.Trunc(budget) != budget || budget < 1024 {
		return status.Error(codes.InvalidArgument, "invalid thinking budget")
	}
	if budget >= float64(config.OutputLimit) {
		return status.Error(codes.InvalidArgument, "thinking budget exceeds admitted output limit")
	}
	return nil
}

func (config ProviderConfig) ValidateIdentityProfile() error {
	if config.ReportedModel == "" {
		return nil
	}
	if len(config.ReportedModel) > 256 || config.APIKey != "" && strings.Contains(config.ReportedModel, config.APIKey) {
		return ErrInvalidAdmission
	}
	for _, value := range config.ReportedModel {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("._-:/", value) {
			continue
		}
		return ErrInvalidAdmission
	}
	return nil
}

func (config ProviderConfig) expectedReportedModel() string {
	if config.ReportedModel != "" {
		return config.ReportedModel
	}
	return config.Model
}

func (config ProviderConfig) ValidateRoutingProfile() error {
	value := config.ProviderSessionID
	if value == "" {
		return nil
	}
	if len(value) > 128 || config.APIKey != "" && strings.Contains(value, config.APIKey) {
		return ErrInvalidAdmission
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-", r) {
			continue
		}
		return ErrInvalidAdmission
	}
	return nil
}

// EvaluationProfile excludes credentials and endpoint text. These are the
// values Go applies to the outgoing request, not provider-side attestation.
func (config ProviderConfig) EvaluationProfile() session.EvaluationModelProfile {
	hash := sha256.Sum256([]byte(config.BaseURL))
	routingHash := ""
	if config.ProviderSessionID != "" {
		hash := sha256.Sum256([]byte(config.ProviderSessionID))
		routingHash = hex.EncodeToString(hash[:])
	}
	return session.EvaluationModelProfile{ResponseMode: config.ResponseMode, ProviderSessionSHA256: routingHash, Protocol: config.Protocol, Model: config.Model, ExpectedReportedModel: config.expectedReportedModel(), ReasoningEffort: config.ReasoningEffort,
		InputLimit: config.InputLimit, OutputLimit: config.OutputLimit, TotalLimit: config.TotalLimit,
		RequestTimeoutSeconds: int64(config.requestTimeout().Seconds()), EndpointSHA256: hex.EncodeToString(hash[:])}
}
