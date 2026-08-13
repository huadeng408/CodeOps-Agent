// Package service contains business logic.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/internal/serverconfig"
	"code-agent/internal/telemetry/genai"
	"code-agent/pkg/log"
	orchestratorclient "code-agent/pkg/orchestrator"

	"github.com/gorilla/websocket"
)

// ChatService defines chat operations.
type ChatService interface {
	StreamResponse(ctx context.Context, query string, user *model.User, ws *websocket.Conn, shouldStop func() bool) error
}

// chatService coordinates retrieval, generation, and conversation persistence.
type chatService struct {
	searchService    SearchService
	memoryService    MemoryService
	conversationRepo repository.ConversationRepository
	orchestrator     orchestratorclient.Client
	evidenceLoader   EvidenceLoader
	userService      UserService
}

// NewChatService creates a new chat service.
func NewChatService(
	searchService SearchService,
	memoryService MemoryService,
	conversationRepo repository.ConversationRepository,
	orchestrator orchestratorclient.Client,
	evidenceLoader EvidenceLoader,
	userService UserService,
) ChatService {
	return &chatService{
		searchService:    searchService,
		memoryService:    memoryService,
		conversationRepo: conversationRepo,
		orchestrator:     orchestrator,
		evidenceLoader:   evidenceLoader,
		userService:      userService,
	}
}

// StreamResponse proxies chat traffic to the external LangGraph orchestrator only.
func (s *chatService) StreamResponse(ctx context.Context, query string, user *model.User, ws *websocket.Conn, shouldStop func() bool) error {
	if s.orchestrator == nil || !s.orchestrator.Enabled() {
		return fmt.Errorf("langgraph orchestrator is disabled")
	}
	if err := s.orchestrator.StreamResponse(ctx, query, user, ws, shouldStop); err != nil {
		return err
	}
	sendCompletion(ws)
	return nil
}

// retrieveContextWithPlan fetches knowledge and memory context in parallel and merges the results.
func (s *chatService) retrieveContextWithPlan(ctx context.Context, query string, plan model.AgentPlan, user *model.User, history []model.ChatMessage) ([]ContextSnippet, error) {
	var (
		knowledgeItems []ContextSnippet
		memoryItems    []ContextSnippet
		knowledgeErr   error
		memoryErr      error
	)

	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		knowledgeItems, knowledgeErr = s.retrieveKnowledgeWithPlan(ctx, query, plan, user)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		if s.memoryService == nil {
			memoryItems = []ContextSnippet{}
			return
		}
		memoryItems, memoryErr = s.memoryService.SearchLongTermMemories(ctx, user, query, plan, history)
	}()
	<-done
	<-done

	if knowledgeErr != nil && len(knowledgeItems) == 0 && memoryErr != nil && len(memoryItems) == 0 {
		return nil, fmt.Errorf("knowledge and memory retrieval both failed: knowledge=%v memory=%v", knowledgeErr, memoryErr)
	}

	if knowledgeErr != nil {
		log.Warnf("knowledge retrieval degraded for query_hash=%s: %v", genai.HashQuery(query), knowledgeErr)
	}
	if memoryErr != nil {
		log.Warnf("memory retrieval degraded for query_hash=%s: %v", genai.HashQuery(query), memoryErr)
	}

	contextItems := append(knowledgeItems, memoryItems...)
	if len(contextItems) == 0 {
		return []ContextSnippet{}, nil
	}

	if s.memoryService != nil {
		fused, err := s.memoryService.FuseContext(ctx, query, contextItems, s.resolveContextTopK())
		if err == nil {
			return fused, nil
		}
		log.Warnf("context fusion degraded for query_hash=%s: %v", genai.HashQuery(query), err)
	}

	sort.SliceStable(contextItems, func(i, j int) bool {
		if contextItems[i].Score == contextItems[j].Score {
			return contextItems[i].Timestamp.After(contextItems[j].Timestamp)
		}
		return contextItems[i].Score > contextItems[j].Score
	})
	if len(contextItems) > s.resolveContextTopK() {
		contextItems = contextItems[:s.resolveContextTopK()]
	}
	return contextItems, nil
}

// retrieveKnowledgeWithPlan executes knowledge retrieval according to the current agent plan.
func (s *chatService) retrieveKnowledgeWithPlan(ctx context.Context, query string, plan model.AgentPlan, user *model.User) ([]ContextSnippet, error) {
	if plan.SkipRetrieval {
		return []ContextSnippet{}, nil
	}

	topK := s.resolveKnowledgeTopK()

	queries := []string{strings.TrimSpace(plan.RewrittenQuery)}
	if queries[0] == "" {
		queries[0] = strings.TrimSpace(query)
	}
	queries = append(queries, plan.SubQueries...)

	disableRerank := false
	if plan.EnableRerank != nil {
		disableRerank = !*plan.EnableRerank
	}

	sharedFusion := s.memoryService != nil
	collected := make([][]model.SearchResponseDTO, 0, len(queries))
	for idx, item := range queries {
		if strings.TrimSpace(item) == "" {
			continue
		}
		opts := SearchOptions{
			Query:         item,
			TopK:          topK,
			Mode:          plan.RetrievalMode,
			DisableRerank: disableRerank || idx > 0 || sharedFusion,
		}
		results, err := s.searchService.Search(ctx, opts, user)
		if err != nil {
			if idx == 0 {
				return nil, err
			}
			log.Warnf("sub-query retrieval degraded, query_hash=%s err=%v", genai.HashQuery(item), err)
			continue
		}
		collected = append(collected, results)
	}

	merged := mergeSearchResults(collected, topK)
	merged = expandEvidenceForUser(ctx, s.evidenceLoader, s.userService, user, merged, query)
	return s.convertSearchResultsToContext(merged), nil
}

// buildContextText formats retrieved snippets into the reference section used by the model prompt.
func (s *chatService) buildContextText(items []ContextSnippet) string {
	if len(items) == 0 {
		return ""
	}
	const maxSnippetLen = 800
	var contextBuilder strings.Builder
	for i, item := range items {
		snippet := item.Text
		if len(snippet) > maxSnippetLen {
			snippet = snippet[:maxSnippetLen] + "..."
		}
		label := item.Label
		if label == "" {
			label = item.SourceType
		}
		contextBuilder.WriteString(fmt.Sprintf("[%d] (%s) %s\n", i+1, label, snippet))
	}
	return contextBuilder.String()
}

// buildSystemMessage assembles the final system prompt with rules, memory, and retrieval context.
func (s *chatService) buildSystemMessage(memoryPrelude, contextText string) string {
	rules := serverconfig.Conf.AI.Prompt.Rules
	if rules == "" {
		rules = serverconfig.Conf.LLM.Prompt.Rules
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

	var sys strings.Builder
	if rules != "" {
		sys.WriteString(rules)
		sys.WriteString("\n\n")
	}
	if strings.TrimSpace(memoryPrelude) != "" {
		sys.WriteString(memoryPrelude)
		sys.WriteString("\n\n")
	}
	sys.WriteString(refStart)
	sys.WriteString("\n")
	if contextText != "" {
		sys.WriteString(contextText)
	} else {
		noRes := serverconfig.Conf.AI.Prompt.NoResultText
		if noRes == "" {
			noRes = serverconfig.Conf.LLM.Prompt.NoResultText
		}
		if noRes == "" {
			noRes = "(no retrieval result in this turn)"
		}
		sys.WriteString(noRes)
		sys.WriteString("\n")
	}
	sys.WriteString(refEnd)
	return sys.String()
}

// loadConversationContext loads the current conversation id and persisted history for the user.
func (s *chatService) loadConversationContext(ctx context.Context, userID uint) (string, []model.ChatMessage, error) {
	convID, err := s.conversationRepo.GetOrCreateConversationID(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	history, err := s.conversationRepo.GetConversationHistory(ctx, convID)
	if err != nil {
		return convID, nil, err
	}
	return convID, history, nil
}

// buildSensoryHistory chooses the short-term history window passed into the model.
func (s *chatService) buildSensoryHistory(history []model.ChatMessage, plan model.AgentPlan) []model.ChatMessage {
	if s.memoryService != nil {
		return s.memoryService.BuildSensoryMemory(history, plan.NeedHistory)
	}
	return s.limitConversationHistory(history, plan.NeedHistory)
}

// limitConversationHistory keeps only the tail of the conversation when history is required.
func (s *chatService) limitConversationHistory(history []model.ChatMessage, needHistory bool) []model.ChatMessage {
	if !needHistory || len(history) == 0 {
		return []model.ChatMessage{}
	}
	const maxHistoryMessages = 8
	if len(history) <= maxHistoryMessages {
		return history
	}
	return history[len(history)-maxHistoryMessages:]
}

// convertSearchResultsToContext maps search hits into generic context snippets.
func (s *chatService) convertSearchResultsToContext(results []model.SearchResponseDTO) []ContextSnippet {
	items := make([]ContextSnippet, 0, len(results))
	for _, result := range results {
		label := result.FileName
		if strings.TrimSpace(label) == "" {
			label = "knowledge"
		}
		items = append(items, ContextSnippet{
			ID:         fmt.Sprintf("%s:%d", result.FileMD5, result.ChunkID),
			SourceType: "knowledge",
			Label:      label,
			Text:       result.TextContent,
			Score:      result.Score,
		})
	}
	return items
}

// resolveKnowledgeTopK computes how many knowledge candidates to fetch before fusion.
func (s *chatService) resolveKnowledgeTopK() int {
	topK := serverconfig.Conf.Retrieval.FinalTopK
	if topK <= 0 {
		topK = 5
	}
	if s.memoryService != nil && serverconfig.Conf.Memory.ContextTopK > topK {
		topK = serverconfig.Conf.Memory.ContextTopK
	}
	if topK < 8 {
		topK = 8
	}
	return topK
}

// resolveContextTopK computes how many fused context snippets to keep.
func (s *chatService) resolveContextTopK() int {
	topK := serverconfig.Conf.Retrieval.FinalTopK
	if topK <= 0 {
		topK = 5
	}
	if serverconfig.Conf.Memory.ContextTopK > 0 {
		topK = maxInt(topK, serverconfig.Conf.Memory.ContextTopK)
	}
	return topK
}

// mergeSearchResults deduplicates chunk hits across sub-queries and keeps the strongest score.
func mergeSearchResults(groups [][]model.SearchResponseDTO, topK int) []model.SearchResponseDTO {
	if len(groups) == 0 {
		return []model.SearchResponseDTO{}
	}

	merged := make(map[string]model.SearchResponseDTO)
	for _, group := range groups {
		for _, item := range group {
			key := fmt.Sprintf("%s:%d", item.FileMD5, item.ChunkID)
			existing, ok := merged[key]
			if !ok || item.Score > existing.Score {
				merged[key] = item
			}
		}
	}

	results := make([]model.SearchResponseDTO, 0, len(merged))
	for _, item := range merged {
		results = append(results, item)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].FileMD5+fmt.Sprint(results[i].ChunkID) < results[j].FileMD5+fmt.Sprint(results[j].ChunkID)
		}
		return results[i].Score > results[j].Score
	})

	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}
	return results
}

// sendCompletion notifies the websocket client that the current response has finished.
func sendCompletion(ws *websocket.Conn) {
	notif := map[string]any{
		"type":      "completion",
		"status":    "finished",
		"message":   "response finished",
		"timestamp": time.Now().UnixMilli(),
		"date":      time.Now().Format("2006-01-02T15:04:05"),
	}
	b, _ := json.Marshal(notif)
	_ = ws.WriteMessage(websocket.TextMessage, b)
}
