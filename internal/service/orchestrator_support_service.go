package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"code-agent/internal/serverconfig"
	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/pkg/log"
	"code-agent/pkg/reranker"
)

// OrchestratorSupportService exposes retrieval and persistence helpers used by the external LangGraph service.
type OrchestratorSupportService interface {
	LoadSession(ctx context.Context, userID uint) (*model.OrchestratorSessionResponse, error)
	RetrieveContext(ctx context.Context, req *model.OrchestratorRetrieveRequest) (*model.OrchestratorRetrieveResponse, error)
	PreparePromptContext(ctx context.Context, req *model.OrchestratorPromptContextRequest) (*model.OrchestratorPromptContextResponse, error)
	SearchKnowledge(ctx context.Context, req *model.OrchestratorKnowledgeSearchRequest) (*model.OrchestratorKnowledgeSearchResponse, error)
	SearchMemory(ctx context.Context, req *model.OrchestratorMemorySearchRequest) (*model.OrchestratorMemorySearchResponse, error)
	RerankContext(ctx context.Context, req *model.OrchestratorRerankRequest) (*model.OrchestratorRerankResponse, error)
	PersistTurn(ctx context.Context, req *model.OrchestratorPersistRequest) error
}

type orchestratorSupportService struct {
	searchService    SearchService
	memoryService    MemoryService
	conversationRepo repository.ConversationRepository
	rerankerClient   reranker.Client
}

// NewOrchestratorSupportService creates a support service for the external LangGraph orchestrator.
func NewOrchestratorSupportService(
	searchService SearchService,
	memoryService MemoryService,
	conversationRepo repository.ConversationRepository,
	rerankerClient reranker.Client,
) OrchestratorSupportService {
	return &orchestratorSupportService{
		searchService:    searchService,
		memoryService:    memoryService,
		conversationRepo: conversationRepo,
		rerankerClient:   rerankerClient,
	}
}

// LoadSession returns the current conversation id and persisted history for a user.
func (s *orchestratorSupportService) LoadSession(ctx context.Context, userID uint) (*model.OrchestratorSessionResponse, error) {
	coordinator := s.newCoordinator()
	conversationID, history, err := coordinator.loadConversationContext(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &model.OrchestratorSessionResponse{
		ConversationID: conversationID,
		History:        history,
	}, nil
}

// RetrieveContext executes retrieval and prompt preparation for a planned query.
func (s *orchestratorSupportService) RetrieveContext(ctx context.Context, req *model.OrchestratorRetrieveRequest) (*model.OrchestratorRetrieveResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("retrieve request is required")
	}

	user := req.User.ToUser()
	if user == nil || user.ID == 0 {
		return nil, fmt.Errorf("user is required")
	}

	coordinator := s.newCoordinator()
	history := append([]model.ChatMessage{}, req.History...)
	conversationID := req.ConversationID
	if conversationID == "" {
		session, err := s.LoadSession(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		conversationID = session.ConversationID
		if len(history) == 0 {
			history = session.History
		}
	}

	knowledgeItems, knowledgeErr := coordinator.retrieveKnowledgeWithPlan(ctx, req.Query, req.Plan, user)
	memoryItems := []ContextSnippet{}
	var memoryErr error
	if s.memoryService != nil {
		memoryItems, memoryErr = s.memoryService.SearchLongTermMemories(ctx, user, req.Query, req.Plan, history)
	}
	if knowledgeErr != nil && len(knowledgeItems) == 0 && memoryErr != nil && len(memoryItems) == 0 {
		return nil, fmt.Errorf("knowledge and memory retrieval both failed: knowledge=%v memory=%v", knowledgeErr, memoryErr)
	}
	if knowledgeErr != nil {
		log.Warnf("[OrchestratorSupportService] knowledge retrieval degraded for query=%q: %v", req.Query, knowledgeErr)
	}
	if memoryErr != nil {
		log.Warnf("[OrchestratorSupportService] memory retrieval degraded for query=%q: %v", req.Query, memoryErr)
	}

	contextItems := append([]ContextSnippet{}, knowledgeItems...)
	contextItems = append(contextItems, memoryItems...)
	if len(contextItems) > 0 {
		if s.memoryService != nil {
			fused, err := s.memoryService.FuseContext(ctx, req.Query, contextItems, coordinator.resolveContextTopK())
			if err == nil {
				contextItems = fused
			} else {
				log.Warnf("[OrchestratorSupportService] context fusion degraded for query=%q: %v", req.Query, err)
				sort.SliceStable(contextItems, func(i, j int) bool {
					if contextItems[i].Score == contextItems[j].Score {
						return contextItems[i].Timestamp.After(contextItems[j].Timestamp)
					}
					return contextItems[i].Score > contextItems[j].Score
				})
				if len(contextItems) > coordinator.resolveContextTopK() {
					contextItems = contextItems[:coordinator.resolveContextTopK()]
				}
			}
		}
	}

	memoryPrelude := ""
	if s.memoryService != nil && conversationID != "" {
		prelude, err := s.memoryService.BuildPrelude(ctx, user.ID, conversationID, history)
		if err != nil {
			log.Warnf("[OrchestratorSupportService] memory prelude degraded for query=%q: %v", req.Query, err)
		} else {
			memoryPrelude = prelude
		}
	}

	contextText := coordinator.buildContextText(contextItems)
	systemMessage := coordinator.buildSystemMessage(memoryPrelude, contextText)

	return &model.OrchestratorRetrieveResponse{
		ConversationID: conversationID,
		History:        history,
		SensoryHistory: coordinator.buildSensoryHistory(history, req.Plan),
		MemoryPrelude:  memoryPrelude,
		KnowledgeItems: convertContextSnippets(knowledgeItems),
		MemoryItems:    convertContextSnippets(memoryItems),
		ContextItems:   convertContextSnippets(contextItems),
		ContextText:    contextText,
		SystemMessage:  systemMessage,
	}, nil
}

// PreparePromptContext computes history windows, memory prelude, and prompt settings without executing retrieval.
func (s *orchestratorSupportService) PreparePromptContext(ctx context.Context, req *model.OrchestratorPromptContextRequest) (*model.OrchestratorPromptContextResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("prompt request is required")
	}
	user := req.User.ToUser()
	if user == nil || user.ID == 0 {
		return nil, fmt.Errorf("user is required")
	}

	coordinator := s.newCoordinator()
	history := append([]model.ChatMessage{}, req.History...)
	conversationID := req.ConversationID
	if conversationID == "" || len(history) == 0 {
		session, err := s.LoadSession(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		if conversationID == "" {
			conversationID = session.ConversationID
		}
		if len(history) == 0 {
			history = session.History
		}
	}

	memoryPrelude := ""
	if s.memoryService != nil && conversationID != "" {
		prelude, err := s.memoryService.BuildPrelude(ctx, user.ID, conversationID, history)
		if err != nil {
			log.Warnf("[OrchestratorSupportService] build prelude degraded for user=%d: %v", user.ID, err)
		} else {
			memoryPrelude = prelude
		}
	}

	promptRules := serverconfig.Conf.AI.Prompt.Rules
	if promptRules == "" {
		promptRules = serverconfig.Conf.LLM.Prompt.Rules
	}
	refStart := serverconfig.Conf.AI.Prompt.RefStart
	if refStart == "" {
		refStart = serverconfig.Conf.LLM.Prompt.RefStart
	}
	if refStart == "" {
		refStart = "<<REF>>"
	}
	refEnd := serverconfig.Conf.AI.Prompt.RefEnd
	if refEnd == "" {
		refEnd = serverconfig.Conf.LLM.Prompt.RefEnd
	}
	if refEnd == "" {
		refEnd = "<<END>>"
	}
	noResultText := serverconfig.Conf.AI.Prompt.NoResultText
	if noResultText == "" {
		noResultText = serverconfig.Conf.LLM.Prompt.NoResultText
	}
	if noResultText == "" {
		noResultText = "(no retrieval result in this turn)"
	}

	return &model.OrchestratorPromptContextResponse{
		ConversationID: conversationID,
		History:        history,
		SensoryHistory: coordinator.buildSensoryHistory(history, req.Plan),
		MemoryPrelude:  memoryPrelude,
		PromptRules:    promptRules,
		RefStart:       refStart,
		RefEnd:         refEnd,
		NoResultText:   noResultText,
		KnowledgeTopK:  coordinator.resolveKnowledgeTopK(),
		ContextTopK:    coordinator.resolveContextTopK(),
		RRFK:           maxInt(serverconfig.Conf.Retrieval.RRFK, 60),
	}, nil
}

// SearchKnowledge exposes one retrieval mode to the external orchestrator.
func (s *orchestratorSupportService) SearchKnowledge(ctx context.Context, req *model.OrchestratorKnowledgeSearchRequest) (*model.OrchestratorKnowledgeSearchResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("knowledge search request is required")
	}
	user := req.User.ToUser()
	if user == nil || user.ID == 0 {
		return nil, fmt.Errorf("user is required")
	}

	results, err := s.searchService.Search(ctx, SearchOptions{
		Query:         req.Query,
		TopK:          req.TopK,
		DisableRerank: req.DisableRerank,
		Mode:          req.Mode,
	}, user)
	if err != nil {
		return nil, err
	}

	return &model.OrchestratorKnowledgeSearchResponse{Results: results}, nil
}

// SearchMemory exposes long-term memory retrieval to the external orchestrator.
func (s *orchestratorSupportService) SearchMemory(ctx context.Context, req *model.OrchestratorMemorySearchRequest) (*model.OrchestratorMemorySearchResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("memory search request is required")
	}
	user := req.User.ToUser()
	if user == nil || user.ID == 0 {
		return nil, fmt.Errorf("user is required")
	}
	if s.memoryService == nil {
		return &model.OrchestratorMemorySearchResponse{Items: []model.OrchestratorContextSnippet{}}, nil
	}

	items, err := s.memoryService.SearchLongTermMemories(ctx, user, req.Query, req.Plan, req.History)
	if err != nil {
		return nil, err
	}

	return &model.OrchestratorMemorySearchResponse{Items: convertContextSnippets(items)}, nil
}

// RerankContext reranks fused snippets for the external orchestrator.
func (s *orchestratorSupportService) RerankContext(ctx context.Context, req *model.OrchestratorRerankRequest) (*model.OrchestratorRerankResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("rerank request is required")
	}
	if strings.TrimSpace(req.Query) == "" {
		return &model.OrchestratorRerankResponse{Items: []model.OrchestratorContextSnippet{}}, nil
	}

	items := convertOrchestratorSnippets(req.Items)
	if len(items) == 0 {
		return &model.OrchestratorRerankResponse{Items: []model.OrchestratorContextSnippet{}}, nil
	}

	topK := req.TopK
	if topK <= 0 {
		topK = s.newCoordinator().resolveContextTopK()
	}

	deduped := dedupeContextSnippets(items)
	if len(deduped) == 0 {
		return &model.OrchestratorRerankResponse{Items: []model.OrchestratorContextSnippet{}}, nil
	}

	if s.rerankerClient != nil && s.rerankerClient.Enabled() {
		docs := make([]reranker.Document, 0, len(deduped))
		for _, item := range deduped {
			docs = append(docs, reranker.Document{
				ID:   item.ID,
				Text: item.Text,
			})
		}
		results, err := s.rerankerClient.Rerank(ctx, req.Query, docs, minInt(topK, len(deduped)))
		if err == nil {
			reranked := make([]ContextSnippet, 0, len(results))
			seen := make(map[int]struct{}, len(results))
			for _, item := range results {
				if item.Index < 0 || item.Index >= len(deduped) {
					continue
				}
				if _, ok := seen[item.Index]; ok {
					continue
				}
				snippet := deduped[item.Index]
				snippet.Score = item.Score
				reranked = append(reranked, snippet)
				seen[item.Index] = struct{}{}
			}
			if len(reranked) > 0 {
				if len(reranked) > topK {
					reranked = reranked[:topK]
				}
				return &model.OrchestratorRerankResponse{Items: convertContextSnippets(reranked)}, nil
			}
		} else {
			log.Warnf("[OrchestratorSupportService] rerank degraded for query=%q: %v", req.Query, err)
		}
	}

	sort.SliceStable(deduped, func(i, j int) bool {
		if deduped[i].Score == deduped[j].Score {
			return deduped[i].Timestamp.After(deduped[j].Timestamp)
		}
		return weightedContextScore(deduped[i]) > weightedContextScore(deduped[j])
	})
	if len(deduped) > topK {
		deduped = deduped[:topK]
	}
	return &model.OrchestratorRerankResponse{Items: convertContextSnippets(deduped)}, nil
}

// PersistTurn stores the final answer in conversation history and writes memory asynchronously.
func (s *orchestratorSupportService) PersistTurn(ctx context.Context, req *model.OrchestratorPersistRequest) error {
	if req == nil {
		return fmt.Errorf("persist request is required")
	}
	if req.User.ID == 0 {
		return fmt.Errorf("user is required")
	}
	if req.Answer == "" {
		return nil
	}

	user := req.User.ToUser()
	conversationID := req.ConversationID
	if conversationID == "" {
		recoveredConversationID, err := s.conversationRepo.GetOrCreateConversationID(ctx, user.ID)
		if err != nil {
			return err
		}
		conversationID = recoveredConversationID
	}

	updatedHistory := append([]model.ChatMessage{}, req.History...)
	updatedHistory = append(updatedHistory,
		model.ChatMessage{Role: "user", Content: req.Query, Timestamp: time.Now()},
		model.ChatMessage{Role: "assistant", Content: req.Answer, Timestamp: time.Now()},
	)

	if err := s.conversationRepo.UpdateConversationHistory(ctx, conversationID, updatedHistory); err != nil {
		return err
	}

	if s.memoryService != nil {
		go func(userCopy *model.User, conversationID string, history []model.ChatMessage, question, answer string) {
			if err := s.memoryService.PersistInteraction(context.Background(), userCopy, conversationID, history, question, answer); err != nil {
				log.Warnf("[OrchestratorSupportService] persist memory degraded for user=%d: %v", userCopy.ID, err)
			}
		}(user, conversationID, updatedHistory, req.Query, req.Answer)
	}

	return nil
}

func (s *orchestratorSupportService) newCoordinator() *chatService {
	return &chatService{
		searchService:    s.searchService,
		memoryService:    s.memoryService,
		conversationRepo: s.conversationRepo,
	}
}

func convertContextSnippets(items []ContextSnippet) []model.OrchestratorContextSnippet {
	if len(items) == 0 {
		return []model.OrchestratorContextSnippet{}
	}

	out := make([]model.OrchestratorContextSnippet, 0, len(items))
	for _, item := range items {
		out = append(out, model.OrchestratorContextSnippet{
			ID:         item.ID,
			SourceType: item.SourceType,
			Label:      item.Label,
			Text:       item.Text,
			Score:      item.Score,
			Timestamp:  item.Timestamp,
		})
	}
	return out
}

func convertOrchestratorSnippets(items []model.OrchestratorContextSnippet) []ContextSnippet {
	if len(items) == 0 {
		return []ContextSnippet{}
	}

	out := make([]ContextSnippet, 0, len(items))
	for _, item := range items {
		out = append(out, ContextSnippet{
			ID:         item.ID,
			SourceType: item.SourceType,
			Label:      item.Label,
			Text:       item.Text,
			Score:      item.Score,
			Timestamp:  item.Timestamp,
		})
	}
	return out
}
