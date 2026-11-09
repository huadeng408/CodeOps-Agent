package permission

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"code-agent/internal/identity"
)

// AuthTier represents the three-level authorization tier in the Go gateway.
type AuthTier string

const (
	TierL0AutoAllow               AuthTier = "L0_AUTO_ALLOW"
	TierL1SessionSigned           AuthTier = "L1_SESSION_SIGNED"
	TierL2HighRiskManualIntercept AuthTier = "L2_HIGH_RISK_MANUAL_INTERCEPT"

	Level0AutoAllow               AuthTier = TierL0AutoAllow
	Level1SessionSigned           AuthTier = TierL1SessionSigned
	Level2HighRiskManualIntercept AuthTier = TierL2HighRiskManualIntercept
)

// AuthState represents the explicit state in the three-level signed authorization state machine.
type AuthState string

const (
	StateInit              AuthState = "INIT"
	StatePendingApproval   AuthState = "PENDING_MANUAL_APPROVAL"
	StateGrantedL0         AuthState = "GRANTED_L0_AUTO"
	StateGrantedL1Session  AuthState = "GRANTED_L1_SESSION_SIGNED"
	StateGrantedL2HighRisk AuthState = "GRANTED_L2_HIGH_RISK_SIGNED"
	StateDeniedPolicy      AuthState = "DENIED_POLICY"
	StateDeniedTampered    AuthState = "DENIED_TAMPERED_SIGNATURE"
	StateExpired           AuthState = "DENIED_EXPIRED_GRANT"
)

// SignedAuthorizationGrant is an HMAC-SHA256 signed authorization token bound to actor, session, tool, and parameters.
type SignedAuthorizationGrant struct {
	GrantID           string    `json:"grant_id"`
	ActorID           string    `json:"actor_id"`
	SessionID         string    `json:"session_id"`
	Tool              string    `json:"tool"`
	ParamDigestSHA256 string    `json:"param_digest_sha256"`
	Tier              AuthTier  `json:"tier"`
	State             AuthState `json:"state"`
	IssuedAt          time.Time `json:"issued_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	HMACSignature     string    `json:"hmac_signature"`
}

// AuthEvaluation records the state machine transition outcome for a tool authorization check.
type AuthEvaluation struct {
	Decision Decision  `json:"decision"`
	Tier     AuthTier  `json:"tier"`
	State    AuthState `json:"state"`
	Reason   string    `json:"reason,omitempty"`
}

func (c *Controller) ensureSigningKeyLocked() ([]byte, error) {
	if len(c.signingKey) == 0 {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate signing key: %w", err)
		}
		c.signingKey = key
	}
	return c.signingKey, nil
}

// TierForTool maps the controller's Level to its canonical AuthTier.
func (c *Controller) TierForTool(tool string) AuthTier {
	switch c.Level(tool) {
	case AutoAllow:
		return TierL0AutoAllow
	case AskSession:
		return TierL1SessionSigned
	default:
		return TierL2HighRiskManualIntercept
	}
}

func canonicalParamsDigest(params map[string]any) string {
	if len(params) == 0 {
		sum := sha256.Sum256([]byte("{}"))
		return hex.EncodeToString(sum[:])
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%v", params)))
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func computeGrantHMAC(key []byte, g SignedAuthorizationGrant) string {
	mac := hmac.New(sha256.New, key)
	payload := strings.Join([]string{
		g.GrantID,
		g.ActorID,
		g.SessionID,
		g.Tool,
		g.ParamDigestSHA256,
		string(g.Tier),
		string(g.State),
		g.IssuedAt.UTC().Format(time.RFC3339Nano),
		g.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}, "|")
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// IssueSignedGrant mints a cryptographically signed grant after session or manual high-risk approval.
func (c *Controller) IssueSignedGrant(
	actor identity.Actor,
	tool string,
	params map[string]any,
	tier AuthTier,
	ttl time.Duration,
) (*SignedAuthorizationGrant, error) {
	if err := actor.Validate(); err != nil {
		return nil, errors.New("invalid actor identity for signed grant")
	}
	trimmedTool := strings.TrimSpace(tool)
	if trimmedTool == "" {
		return nil, errors.New("tool name is required")
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	now := time.Now().UTC()
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate grant nonce: %w", err)
	}

	state := StateGrantedL1Session
	if tier == TierL2HighRiskManualIntercept {
		state = StateGrantedL2HighRisk
	} else if tier == TierL0AutoAllow {
		state = StateGrantedL0
	}

	grant := SignedAuthorizationGrant{
		GrantID:           hex.EncodeToString(nonce),
		ActorID:           actor.ActorID,
		SessionID:         actor.SessionID,
		Tool:              trimmedTool,
		ParamDigestSHA256: canonicalParamsDigest(params),
		Tier:              tier,
		State:             state,
		IssuedAt:          now,
		ExpiresAt:         now.Add(ttl),
	}

	c.mu.Lock()
	key, err := c.ensureSigningKeyLocked()
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	grant.HMACSignature = computeGrantHMAC(key, grant)
	c.mu.Unlock()
	return &grant, nil
}

// EvaluateWithGrant runs the three-level signed authorization state machine for a tool invocation.
func (c *Controller) EvaluateWithGrant(
	actor identity.Actor,
	tool string,
	params map[string]any,
	grant *SignedAuthorizationGrant,
) AuthEvaluation {
	if err := actor.Validate(); err != nil {
		return AuthEvaluation{
			Decision: AskUser,
			Tier:     TierL2HighRiskManualIntercept,
			State:    StatePendingApproval,
			Reason:   "unauthenticated actor requires manual intercept",
		}
	}

	c.mu.Lock()
	for _, rule := range c.denylist {
		if rule.Matches(tool, params) {
			c.mu.Unlock()
			return AuthEvaluation{
				Decision: Deny,
				Tier:     TierL2HighRiskManualIntercept,
				State:    StateDeniedPolicy,
				Reason:   "matched denylist policy",
			}
		}
	}
	for _, rule := range c.allowlist {
		if rule.Matches(tool, params) {
			c.mu.Unlock()
			return AuthEvaluation{
				Decision: Approve,
				Tier:     TierL0AutoAllow,
				State:    StateGrantedL0,
				Reason:   "matched allowlist policy",
			}
		}
	}
	level := c.levelLocked(tool)
	key, keyErr := c.ensureSigningKeyLocked()
	c.mu.Unlock()
	if keyErr != nil {
		return AuthEvaluation{
			Decision: Deny,
			Tier:     TierL2HighRiskManualIntercept,
			State:    StateDeniedPolicy,
			Reason:   "signing key unavailable",
		}
	}

	var requiredTier AuthTier
	switch level {
	case AutoAllow:
		return AuthEvaluation{
			Decision: Approve,
			Tier:     TierL0AutoAllow,
			State:    StateGrantedL0,
		}
	case AskSession:
		requiredTier = TierL1SessionSigned
	default:
		requiredTier = TierL2HighRiskManualIntercept
	}

	if grant == nil {
		return AuthEvaluation{
			Decision: AskUser,
			Tier:     requiredTier,
			State:    StatePendingApproval,
			Reason:   "signed authorization grant required",
		}
	}

	expectedSig := computeGrantHMAC(key, *grant)
	if !hmac.Equal([]byte(expectedSig), []byte(grant.HMACSignature)) {
		return AuthEvaluation{
			Decision: Deny,
			Tier:     requiredTier,
			State:    StateDeniedTampered,
			Reason:   "HMAC signature verification failed",
		}
	}
	if time.Now().UTC().After(grant.ExpiresAt) {
		return AuthEvaluation{
			Decision: Deny,
			Tier:     requiredTier,
			State:    StateExpired,
			Reason:   "signed authorization grant expired",
		}
	}
	if grant.ActorID != actor.ActorID || grant.SessionID != actor.SessionID || grant.Tool != strings.TrimSpace(tool) {
		return AuthEvaluation{
			Decision: Deny,
			Tier:     requiredTier,
			State:    StateDeniedTampered,
			Reason:   "grant actor, session, or tool scope mismatch",
		}
	}
	if requiredTier == TierL2HighRiskManualIntercept {
		if grant.Tier != TierL2HighRiskManualIntercept {
			return AuthEvaluation{
				Decision: AskUser,
				Tier:     requiredTier,
				State:    StatePendingApproval,
				Reason:   "high-risk operation requires L2 manual intercept grant",
			}
		}
		if grant.ParamDigestSHA256 != canonicalParamsDigest(params) {
			return AuthEvaluation{
				Decision: Deny,
				Tier:     requiredTier,
				State:    StateDeniedTampered,
				Reason:   "high-risk parameter digest mismatch",
			}
		}
		return AuthEvaluation{
			Decision: Approve,
			Tier:     TierL2HighRiskManualIntercept,
			State:    StateGrantedL2HighRisk,
		}
	}

	return AuthEvaluation{
		Decision: Approve,
		Tier:     TierL1SessionSigned,
		State:    StateGrantedL1Session,
	}
}
