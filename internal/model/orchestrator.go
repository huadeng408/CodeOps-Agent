package model

import "time"

// OrchestratorUser captures the subset of user fields exchanged with the external AI orchestrator.
type OrchestratorUser struct {
	ID         uint   `json:"id"`
	Username   string `json:"username"`
	Role       string `json:"role"`
	OrgTags    string `json:"orgTags"`
	PrimaryOrg string `json:"primaryOrg"`
}

// NewOrchestratorUser converts a persisted user into the transport payload used by the orchestrator service.
func NewOrchestratorUser(user *User) OrchestratorUser {
	if user == nil {
		return OrchestratorUser{}
	}
	return OrchestratorUser{
		ID:         user.ID,
		Username:   user.Username,
		Role:       user.Role,
		OrgTags:    user.OrgTags,
		PrimaryOrg: user.PrimaryOrg,
	}
}

// ToUser converts the transport payload back into the domain model shape used by services.
func (u OrchestratorUser) ToUser() *User {
	return &User{
		ID:         u.ID,
		Username:   u.Username,
		Role:       u.Role,
		OrgTags:    u.OrgTags,
		PrimaryOrg: u.PrimaryOrg,
	}
}

// OrchestratorContextSnippet mirrors one retrieval snippet returned to the orchestrator.
type OrchestratorContextSnippet struct {
	ID         string    `json:"id"`
	SourceType string    `json:"sourceType"`
	Label      string    `json:"label"`
	Text       string    `json:"text"`
	Score      float64   `json:"score"`
	Timestamp  time.Time `json:"timestamp"`
}

// OrchestratorSessionRequest requests the persisted session snapshot for a user.
type OrchestratorSessionRequest struct {
	UserID uint `json:"userId"`
}

// OrchestratorSessionResponse returns conversation metadata and history to the orchestrator.
type OrchestratorSessionResponse struct {
	ConversationID string        `json:"conversationId"`
	History        []ChatMessage `json:"history"`
}

// OrchestratorRetrieveRequest asks the Go service to execute retrieval for a planned query.
type OrchestratorRetrieveRequest struct {
	User           OrchestratorUser `json:"user"`
	Query          string           `json:"query"`
	ConversationID string           `json:"conversationId"`
	History        []ChatMessage    `json:"history"`
	Plan           AgentPlan        `json:"plan"`
}

// OrchestratorRetrieveResponse provides the prompt-building materials used by the LangGraph service.
type OrchestratorRetrieveResponse struct {
	ConversationID string                       `json:"conversationId"`
	History        []ChatMessage                `json:"history"`
	SensoryHistory []ChatMessage                `json:"sensoryHistory"`
	MemoryPrelude  string                       `json:"memoryPrelude"`
	KnowledgeItems []OrchestratorContextSnippet `json:"knowledgeItems"`
	MemoryItems    []OrchestratorContextSnippet `json:"memoryItems"`
	ContextItems   []OrchestratorContextSnippet `json:"contextItems"`
	ContextText    string                       `json:"contextText"`
	SystemMessage  string                       `json:"systemMessage"`
}

// OrchestratorPromptContextRequest asks Go to prepare prompt-related state without executing retrieval.
type OrchestratorPromptContextRequest struct {
	User           OrchestratorUser `json:"user"`
	ConversationID string           `json:"conversationId"`
	History        []ChatMessage    `json:"history"`
	Plan           AgentPlan        `json:"plan"`
}

// OrchestratorPromptContextResponse returns prompt settings and memory prelude for the Python orchestrator.
type OrchestratorPromptContextResponse struct {
	ConversationID string        `json:"conversationId"`
	History        []ChatMessage `json:"history"`
	SensoryHistory []ChatMessage `json:"sensoryHistory"`
	MemoryPrelude  string        `json:"memoryPrelude"`
	PromptRules    string        `json:"promptRules"`
	RefStart       string        `json:"refStart"`
	RefEnd         string        `json:"refEnd"`
	NoResultText   string        `json:"noResultText"`
	KnowledgeTopK  int           `json:"knowledgeTopK"`
	ContextTopK    int           `json:"contextTopK"`
	RRFK           int           `json:"rrfK"`
}

// OrchestratorKnowledgeSearchRequest asks Go to execute one knowledge retrieval mode.
type OrchestratorKnowledgeSearchRequest struct {
	User          OrchestratorUser `json:"user"`
	Query         string           `json:"query"`
	TopK          int              `json:"topK"`
	DisableRerank bool             `json:"disableRerank"`
	Mode          RetrievalMode    `json:"mode"`
}

// OrchestratorKnowledgeSearchResponse returns knowledge retrieval results.
type OrchestratorKnowledgeSearchResponse struct {
	Results []SearchResponseDTO `json:"results"`
}

// OrchestratorMemorySearchRequest asks Go to search long-term memory.
type OrchestratorMemorySearchRequest struct {
	User    OrchestratorUser `json:"user"`
	Query   string           `json:"query"`
	History []ChatMessage    `json:"history"`
	Plan    AgentPlan        `json:"plan"`
}

// OrchestratorMemorySearchResponse returns memory retrieval snippets.
type OrchestratorMemorySearchResponse struct {
	Items []OrchestratorContextSnippet `json:"items"`
}

// OrchestratorRerankRequest asks Go to rerank fused context snippets.
type OrchestratorRerankRequest struct {
	Query string                       `json:"query"`
	TopK  int                          `json:"topK"`
	Items []OrchestratorContextSnippet `json:"items"`
}

// OrchestratorRerankResponse returns reranked context snippets.
type OrchestratorRerankResponse struct {
	Items []OrchestratorContextSnippet `json:"items"`
}

// OrchestratorProfileUpdate represents one profile-slot update returned by the orchestrator.
type OrchestratorProfileUpdate struct {
	SlotKey    string  `json:"slot_key"`
	SlotValue  string  `json:"slot_value"`
	Confidence float64 `json:"confidence"`
}

// OrchestratorMemorySummaryRequest asks the orchestrator to summarize recent dialogue into working memory.
type OrchestratorMemorySummaryRequest struct {
	History                []ChatMessage `json:"history"`
	WorkingHistoryMessages int           `json:"workingHistoryMessages"`
	WorkingMaxFacts        int           `json:"workingMaxFacts"`
}

// OrchestratorMemorySummaryResponse returns the structured working-memory summary.
type OrchestratorMemorySummaryResponse struct {
	Summary        string                      `json:"summary"`
	Facts          []string                    `json:"facts"`
	Entities       []string                    `json:"entities"`
	ProfileUpdates []OrchestratorProfileUpdate `json:"profile_updates"`
}

// OrchestratorMemoryWriteRequest asks the orchestrator whether the latest turn should become long-term memory.
type OrchestratorMemoryWriteRequest struct {
	Question       string `json:"question"`
	Answer         string `json:"answer"`
	WorkingSummary string `json:"workingSummary"`
}

// OrchestratorMemoryWriteResponse returns the structured long-term-memory decision.
type OrchestratorMemoryWriteResponse struct {
	ShouldStore    bool                        `json:"should_store"`
	MemoryType     string                      `json:"memory_type"`
	Summary        string                      `json:"summary"`
	Content        string                      `json:"content"`
	Entities       []string                    `json:"entities"`
	Importance     float64                     `json:"importance"`
	ProfileUpdates []OrchestratorProfileUpdate `json:"profile_updates"`
}

// OrchestratorPersistRequest asks the Go service to persist a completed chat turn.
type OrchestratorPersistRequest struct {
	User           OrchestratorUser `json:"user"`
	ConversationID string           `json:"conversationId"`
	History        []ChatMessage    `json:"history"`
	Query          string           `json:"query"`
	Answer         string           `json:"answer"`
}
