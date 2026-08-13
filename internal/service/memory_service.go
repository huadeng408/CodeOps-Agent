// Package service contains business logic.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/internal/serverconfig"
	"code-agent/internal/telemetry/genai"
	"code-agent/pkg/embedding"
	"code-agent/pkg/es"
	"code-agent/pkg/log"
	orchestratorclient "code-agent/pkg/orchestrator"
	"code-agent/pkg/reranker"

	"github.com/elastic/go-elasticsearch/v8"
	"gorm.io/gorm"
)

// MemoryService defines the working-memory, profile, and long-term-memory operations used during chat.
type MemoryService interface {
	BuildSensoryMemory(history []model.ChatMessage, needHistory bool) []model.ChatMessage
	BuildPrelude(ctx context.Context, userID uint, conversationID string, history []model.ChatMessage) (string, error)
	SearchLongTermMemories(ctx context.Context, user *model.User, query string, plan model.AgentPlan, history []model.ChatMessage) ([]ContextSnippet, error)
	FuseContext(ctx context.Context, query string, items []ContextSnippet, topK int) ([]ContextSnippet, error)
	PersistInteraction(ctx context.Context, user *model.User, conversationID string, history []model.ChatMessage, question, answer string) error
}

// memoryService implements prompt-time memory retrieval and post-turn memory persistence.
type memoryService struct {
	repo            repository.MemoryRepository
	embeddingClient embedding.Client
	memoryClient    orchestratorclient.MemoryClient
	rerankerClient  reranker.Client
	esClient        *elasticsearch.Client
	cfg             serverconfig.MemoryConfig
}

// memorySummaryPayload describes the structured working-memory summary returned by the planner.
type memorySummaryPayload struct {
	Summary        string                `json:"summary"`
	Facts          []string              `json:"facts"`
	Entities       []string              `json:"entities"`
	ProfileUpdates []profileUpdateRecord `json:"profile_updates"`
}

// memoryWritePayload describes the structured long-term-memory decision returned by the planner.
type memoryWritePayload struct {
	ShouldStore    bool                  `json:"should_store"`
	MemoryType     string                `json:"memory_type"`
	Summary        string                `json:"summary"`
	Content        string                `json:"content"`
	Entities       []string              `json:"entities"`
	Importance     float64               `json:"importance"`
	ProfileUpdates []profileUpdateRecord `json:"profile_updates"`
}

// profileUpdateRecord stores one candidate profile slot update extracted from dialogue.
type profileUpdateRecord struct {
	SlotKey    string  `json:"slot_key"`
	SlotValue  string  `json:"slot_value"`
	Confidence float64 `json:"confidence"`
}

// memoryRetrievalHit stores one memory search hit together with its fusion score.
type memoryRetrievalHit struct {
	ID     string
	Score  float64
	Source model.MemoryEsDocument
}

// memorySearchResponse mirrors the subset of the Elasticsearch response used by memory retrieval.
type memorySearchResponse struct {
	Hits struct {
		Hits []struct {
			ID     string                 `json:"_id"`
			Score  float64                `json:"_score"`
			Source model.MemoryEsDocument `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

var jsonObjectPattern = regexp.MustCompile(`\{[\s\S]*\}`)

// NewMemoryService creates a memory service with normalized runtime configuration.
func NewMemoryService(
	repo repository.MemoryRepository,
	embeddingClient embedding.Client,
	memoryClient orchestratorclient.MemoryClient,
	rerankerClient reranker.Client,
	esClient *elasticsearch.Client,
	cfg serverconfig.MemoryConfig,
) MemoryService {
	return &memoryService{
		repo:            repo,
		embeddingClient: embeddingClient,
		memoryClient:    memoryClient,
		rerankerClient:  rerankerClient,
		esClient:        esClient,
		cfg:             normalizeMemoryConfig(cfg),
	}
}

// normalizeMemoryConfig fills in conservative defaults for the memory subsystem.
func normalizeMemoryConfig(cfg serverconfig.MemoryConfig) serverconfig.MemoryConfig {
	if cfg.SensoryMaxMessages <= 0 {
		cfg.SensoryMaxMessages = 6
	}
	if cfg.SensoryMaxTokens <= 0 {
		cfg.SensoryMaxTokens = 800
	}
	if cfg.WorkingTriggerMessages <= 0 {
		cfg.WorkingTriggerMessages = 8
	}
	if cfg.WorkingMaxFacts <= 0 {
		cfg.WorkingMaxFacts = 6
	}
	if cfg.WorkingHistoryMessages <= 0 {
		cfg.WorkingHistoryMessages = 12
	}
	if cfg.ProfileMaxSlots <= 0 {
		cfg.ProfileMaxSlots = 6
	}
	if cfg.LongTermTopK <= 0 {
		cfg.LongTermTopK = 4
	}
	if cfg.ContextTopK <= 0 {
		cfg.ContextTopK = 6
	}
	if cfg.LongTermMinImportance <= 0 {
		cfg.LongTermMinImportance = 0.45
	}
	if strings.TrimSpace(cfg.MemoryIndexName) == "" {
		cfg.MemoryIndexName = "conversation_memory"
	}
	return cfg
}

// BuildSensoryMemory trims recent dialogue history into a compact short-term memory window.
func (s *memoryService) BuildSensoryMemory(history []model.ChatMessage, needHistory bool) []model.ChatMessage {
	if !needHistory || len(history) == 0 {
		return []model.ChatMessage{}
	}

	selected := make([]model.ChatMessage, 0, minInt(len(history), s.cfg.SensoryMaxMessages))
	totalTokens := 0
	maxMessages := minInt(len(history), s.cfg.SensoryMaxMessages)
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		msgTokens := estimateTextTokens(msg.Content) + 8
		if len(selected) >= maxMessages {
			break
		}
		if len(selected) > 0 && totalTokens+msgTokens > s.cfg.SensoryMaxTokens {
			break
		}
		totalTokens += msgTokens
		selected = append(selected, msg)
	}

	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return selected
}

// BuildPrelude renders profile and working-memory sections that are prepended to the model prompt.
func (s *memoryService) BuildPrelude(ctx context.Context, userID uint, conversationID string, history []model.ChatMessage) (string, error) {
	if !s.cfg.Enabled {
		return "", nil
	}

	var sections []string

	profileText, err := s.renderProfile(ctx, userID)
	if err != nil {
		return "", err
	}
	if profileText != "" {
		sections = append(sections, "## User Profile\n"+profileText)
	}

	snapshot, err := s.loadOrRefreshWorkingSnapshot(ctx, userID, conversationID, history)
	if err != nil {
		return "", err
	}
	if snapshot != nil {
		sections = append(sections, formatWorkingSnapshot(snapshot))
	}

	return strings.TrimSpace(strings.Join(sections, "\n\n")), nil
}

// SearchLongTermMemories retrieves long-term memories that may help answer the current turn.
func (s *memoryService) SearchLongTermMemories(ctx context.Context, user *model.User, query string, plan model.AgentPlan, history []model.ChatMessage) ([]ContextSnippet, error) {
	if !s.cfg.Enabled || user == nil || s.esClient == nil {
		return []ContextSnippet{}, nil
	}
	if !shouldRetrieveLongTermMemory(query, plan, history) {
		return []ContextSnippet{}, nil
	}

	topN := maxInt(s.cfg.LongTermTopK*2, s.cfg.LongTermTopK)
	var (
		textHits   []memoryRetrievalHit
		vectorHits []memoryRetrievalHit
		textErr    error
		vectorErr  error
	)

	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		textHits, textErr = s.memoryTextSearch(ctx, user.ID, query, topN)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		vectorHits, vectorErr = s.memoryVectorSearch(ctx, user.ID, query, topN)
	}()
	<-done
	<-done

	if textErr != nil {
		log.Warnf("[MemoryService] text memory search degraded for user=%d query_hash=%s: %v", user.ID, genai.HashQuery(query), textErr)
	}
	if vectorErr != nil {
		log.Warnf("[MemoryService] vector memory search degraded for user=%d query_hash=%s: %v", user.ID, genai.HashQuery(query), vectorErr)
	}

	hits := rrfFuseMemoryHits(60, textHits, vectorHits)
	if len(hits) == 0 {
		return []ContextSnippet{}, nil
	}

	snippets := make([]ContextSnippet, 0, minInt(len(hits), s.cfg.LongTermTopK))
	for _, hit := range hits {
		snippets = append(snippets, ContextSnippet{
			ID:         hit.Source.MemoryID,
			SourceType: "memory",
			Label:      formatMemoryLabel(hit.Source),
			Text:       hit.Source.TextContent,
			Score:      hit.Score,
			Timestamp:  hit.Source.CreatedAt,
		})
		if len(snippets) >= s.cfg.LongTermTopK {
			break
		}
	}
	return snippets, nil
}

// FuseContext deduplicates and reranks mixed knowledge and memory snippets.
func (s *memoryService) FuseContext(ctx context.Context, query string, items []ContextSnippet, topK int) ([]ContextSnippet, error) {
	if len(items) == 0 {
		return []ContextSnippet{}, nil
	}
	if topK <= 0 {
		topK = s.cfg.ContextTopK
	}

	deduped := dedupeContextSnippets(items)
	if len(deduped) == 0 {
		return []ContextSnippet{}, nil
	}

	if s.rerankerClient != nil && s.rerankerClient.Enabled() {
		docs := make([]reranker.Document, 0, len(deduped))
		for _, item := range deduped {
			docs = append(docs, reranker.Document{
				ID:   item.ID,
				Text: item.Text,
			})
		}

		results, err := s.rerankerClient.Rerank(ctx, query, docs, minInt(topK, len(deduped)))
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
				return reranked, nil
			}
		} else {
			log.Warnf("[MemoryService] context rerank degraded for query_hash=%s: %v", genai.HashQuery(query), err)
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
	return deduped, nil
}

// PersistInteraction updates working memory, profile slots, and optional long-term memory after a chat turn.
func (s *memoryService) PersistInteraction(ctx context.Context, user *model.User, conversationID string, history []model.ChatMessage, question, answer string) error {
	if !s.cfg.Enabled || user == nil {
		return nil
	}

	snapshot, err := s.loadOrRefreshWorkingSnapshot(ctx, user.ID, conversationID, history)
	if err != nil {
		return err
	}

	payload, err := s.extractLongTermMemory(ctx, question, answer, snapshot)
	if err != nil {
		log.Warnf("[MemoryService] long-term memory extraction degraded for user=%d: %v", user.ID, err)
		payload = heuristicMemoryWritePayload(question, answer)
	}

	if err := s.applyProfileUpdates(ctx, user.ID, payload.ProfileUpdates, "long_term_memory"); err != nil {
		log.Warnf("[MemoryService] apply profile updates degraded for user=%d: %v", user.ID, err)
	}

	if !payload.ShouldStore || payload.Importance < s.cfg.LongTermMinImportance {
		return nil
	}

	content := strings.TrimSpace(payload.Content)
	if content == "" {
		content = strings.TrimSpace(question + "\n" + answer)
	}
	summary := strings.TrimSpace(payload.Summary)
	if summary == "" {
		summary = truncateRunes(content, 240)
	}
	entities := normalizeStringList(payload.Entities)
	entitiesJSON, _ := json.Marshal(entities)

	memoryID := fmt.Sprintf("%d-%d", time.Now().UnixNano(), user.ID)
	entry := &model.LongTermMemory{
		MemoryID:       memoryID,
		UserID:         user.ID,
		ConversationID: conversationID,
		MemoryType:     normalizeMemoryType(payload.MemoryType),
		Content:        content,
		Summary:        summary,
		EntitiesJSON:   string(entitiesJSON),
		Importance:     payload.Importance,
	}
	if err := s.repo.CreateLongTermMemory(entry); err != nil {
		return err
	}

	if s.embeddingClient == nil || s.esClient == nil {
		return nil
	}

	vector, err := s.embeddingClient.CreateEmbedding(ctx, summary)
	if err != nil {
		return nil
	}

	doc := model.MemoryEsDocument{
		MemoryID:       memoryID,
		UserID:         user.ID,
		ConversationID: conversationID,
		MemoryType:     entry.MemoryType,
		TextContent:    summary,
		Vector:         vector,
		Importance:     entry.Importance,
		CreatedAt:      entry.CreatedAt,
	}
	return es.IndexMemoryDocument(ctx, s.cfg.MemoryIndexName, doc)
}

// renderProfile renders profile.
func (s *memoryService) renderProfile(ctx context.Context, userID uint) (string, error) {
	slots, err := s.repo.ListProfileSlots(userID, s.cfg.ProfileMaxSlots)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", err
	}
	if len(slots) == 0 {
		return "", nil
	}

	var lines []string
	for _, slot := range slots {
		if strings.TrimSpace(slot.SlotValue) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", slot.SlotKey, slot.SlotValue))
	}
	return strings.Join(lines, "\n"), nil
}

// loadOrRefreshWorkingSnapshot loads or refresh working snapshot.
func (s *memoryService) loadOrRefreshWorkingSnapshot(ctx context.Context, userID uint, conversationID string, history []model.ChatMessage) (*model.WorkingMemorySnapshot, error) {
	if len(history) < s.cfg.WorkingTriggerMessages {
		return nil, nil
	}

	snapshot, err := s.repo.GetWorkingSnapshot(userID, conversationID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	if snapshot != nil && len(history)-snapshot.MessageCount < 4 {
		return snapshot, nil
	}

	payload, err := s.summarizeWorkingMemory(ctx, history)
	if err != nil {
		log.Warnf("[MemoryService] summarize working memory degraded for user=%d conversation=%s: %v", userID, conversationID, err)
		payload = heuristicWorkingSummary(history, s.cfg.WorkingMaxFacts)
	}

	if err := s.applyProfileUpdates(ctx, userID, payload.ProfileUpdates, "working_memory"); err != nil {
		log.Warnf("[MemoryService] apply working-memory profile updates degraded for user=%d: %v", userID, err)
	}

	facts := normalizeStringList(payload.Facts)
	if len(facts) > s.cfg.WorkingMaxFacts {
		facts = facts[:s.cfg.WorkingMaxFacts]
	}
	entities := normalizeStringList(payload.Entities)
	factsJSON, _ := json.Marshal(facts)
	entitiesJSON, _ := json.Marshal(entities)

	newSnapshot := &model.WorkingMemorySnapshot{
		UserID:         userID,
		ConversationID: conversationID,
		Summary:        strings.TrimSpace(payload.Summary),
		FactsJSON:      string(factsJSON),
		EntitiesJSON:   string(entitiesJSON),
		MessageCount:   len(history),
	}
	if newSnapshot.Summary == "" {
		newSnapshot.Summary = truncateRunes(renderConversationForPrompt(limitHistoryMessages(history, s.cfg.WorkingHistoryMessages)), 400)
	}
	if err := s.repo.UpsertWorkingSnapshot(newSnapshot); err != nil {
		return nil, err
	}
	return s.repo.GetWorkingSnapshot(userID, conversationID)
}

// summarizeWorkingMemory handles summarize working memory.
func (s *memoryService) summarizeWorkingMemory(ctx context.Context, history []model.ChatMessage) (*memorySummaryPayload, error) {
	if s.memoryClient == nil || !s.memoryClient.Enabled() {
		return nil, fmt.Errorf("orchestrator memory client is disabled")
	}

	resp, err := s.memoryClient.SummarizeWorkingMemory(
		ctx,
		history,
		s.cfg.WorkingHistoryMessages,
		s.cfg.WorkingMaxFacts,
	)
	if err != nil {
		return nil, err
	}

	payload := memorySummaryPayload{
		Summary:  resp.Summary,
		Facts:    append([]string{}, resp.Facts...),
		Entities: append([]string{}, resp.Entities...),
	}
	for _, item := range resp.ProfileUpdates {
		payload.ProfileUpdates = append(payload.ProfileUpdates, profileUpdateRecord{
			SlotKey:    item.SlotKey,
			SlotValue:  item.SlotValue,
			Confidence: item.Confidence,
		})
	}
	payload.Facts = normalizeStringList(payload.Facts)
	payload.Entities = normalizeStringList(payload.Entities)
	payload.ProfileUpdates = normalizeProfileUpdates(payload.ProfileUpdates)
	return &payload, nil
}

// extractLongTermMemory extracts long term memory.
func (s *memoryService) extractLongTermMemory(ctx context.Context, question, answer string, snapshot *model.WorkingMemorySnapshot) (*memoryWritePayload, error) {
	if s.memoryClient == nil || !s.memoryClient.Enabled() {
		return nil, fmt.Errorf("orchestrator memory client is disabled")
	}

	workingSummary := ""
	if snapshot != nil {
		workingSummary = strings.TrimSpace(snapshot.Summary)
	}
	resp, err := s.memoryClient.ExtractLongTermMemory(ctx, question, answer, workingSummary)
	if err != nil {
		return nil, err
	}

	payload := memoryWritePayload{
		ShouldStore: resp.ShouldStore,
		MemoryType:  resp.MemoryType,
		Summary:     resp.Summary,
		Content:     resp.Content,
		Entities:    append([]string{}, resp.Entities...),
		Importance:  resp.Importance,
	}
	for _, item := range resp.ProfileUpdates {
		payload.ProfileUpdates = append(payload.ProfileUpdates, profileUpdateRecord{
			SlotKey:    item.SlotKey,
			SlotValue:  item.SlotValue,
			Confidence: item.Confidence,
		})
	}
	payload.MemoryType = normalizeMemoryType(payload.MemoryType)
	payload.Entities = normalizeStringList(payload.Entities)
	payload.ProfileUpdates = normalizeProfileUpdates(payload.ProfileUpdates)
	if payload.Importance < 0 {
		payload.Importance = 0
	}
	if payload.Importance > 1 {
		payload.Importance = 1
	}
	return &payload, nil
}

// applyProfileUpdates applies profile updates.
func (s *memoryService) applyProfileUpdates(ctx context.Context, userID uint, updates []profileUpdateRecord, source string) error {
	now := time.Now()
	for _, update := range normalizeProfileUpdates(updates) {
		key := normalizeProfileSlotKey(update.SlotKey)
		if key == "" || strings.TrimSpace(update.SlotValue) == "" {
			continue
		}

		incoming := &model.UserProfileSlot{
			UserID:     userID,
			SlotKey:    key,
			SlotValue:  truncateRunes(strings.TrimSpace(update.SlotValue), 255),
			Confidence: clampFloat(update.Confidence, 0.1, 0.99),
			Source:     source,
		}

		existing, err := s.repo.GetProfileSlot(userID, key)
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if existing == nil {
			if err := s.repo.UpsertProfileSlot(incoming); err != nil {
				return err
			}
			continue
		}

		if strings.EqualFold(existing.SlotValue, incoming.SlotValue) {
			existing.Confidence = math.Max(existing.Confidence, incoming.Confidence)
			existing.Source = incoming.Source
			existing.UpdatedAt = now
			if err := s.repo.UpsertProfileSlot(existing); err != nil {
				return err
			}
			continue
		}

		ageDays := math.Max(0, now.Sub(existing.UpdatedAt).Hours()/24)
		decayedConfidence := existing.Confidence * math.Exp(-ageDays/30.0)
		if incoming.Confidence >= decayedConfidence {
			existing.SlotValue = incoming.SlotValue
			existing.Confidence = incoming.Confidence
			existing.Source = incoming.Source
			existing.UpdatedAt = now
			if err := s.repo.UpsertProfileSlot(existing); err != nil {
				return err
			}
		}
	}
	return nil
}

// memoryTextSearch handles memory text search.
func (s *memoryService) memoryTextSearch(ctx context.Context, userID uint, query string, topN int) ([]memoryRetrievalHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []memoryRetrievalHit{}, nil
	}

	body := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"must": []any{
					map[string]any{
						"match": map[string]any{
							"text_content": map[string]any{
								"query": query,
							},
						},
					},
				},
				"filter": []any{
					map[string]any{"term": map[string]any{"user_id": userID}},
				},
			},
		},
		"size":             topN,
		"track_total_hits": false,
	}
	return s.searchMemoriesOnce(ctx, body)
}

// memoryVectorSearch handles memory vector search.
func (s *memoryService) memoryVectorSearch(ctx context.Context, userID uint, query string, topN int) ([]memoryRetrievalHit, error) {
	if s.embeddingClient == nil {
		return []memoryRetrievalHit{}, nil
	}
	queryVector, err := s.embeddingClient.CreateEmbedding(ctx, query)
	if err != nil {
		return nil, err
	}

	body := map[string]any{
		"knn": map[string]any{
			"field":          "vector",
			"query_vector":   queryVector,
			"k":              topN,
			"num_candidates": maxInt(topN*4, topN),
			"filter": map[string]any{
				"term": map[string]any{
					"user_id": userID,
				},
			},
		},
		"size":             topN,
		"track_total_hits": false,
	}
	return s.searchMemoriesOnce(ctx, body)
}

// searchMemoriesOnce searches memories once.
func (s *memoryService) searchMemoriesOnce(ctx context.Context, body map[string]any) ([]memoryRetrievalHit, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, err
	}

	res, err := s.esClient.Search(
		s.esClient.Search.WithContext(ctx),
		s.esClient.Search.WithIndex(s.cfg.MemoryIndexName),
		s.esClient.Search.WithBody(&buf),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.IsError() {
		raw, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("memory search failed: status=%s body_bytes=%d", res.Status(), len(raw))
	}

	var parsed memorySearchResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	hits := make([]memoryRetrievalHit, 0, len(parsed.Hits.Hits))
	for _, hit := range parsed.Hits.Hits {
		hits = append(hits, memoryRetrievalHit{
			ID:     hit.ID,
			Score:  hit.Score,
			Source: hit.Source,
		})
	}
	return hits, nil
}

// rrfFuseMemoryHits handles rrf fuse memory hits.
func rrfFuseMemoryHits(rrfK int, hitLists ...[]memoryRetrievalHit) []memoryRetrievalHit {
	type fusedEntry struct {
		hit   memoryRetrievalHit
		score float64
	}

	fused := make(map[string]*fusedEntry)
	for _, hits := range hitLists {
		for idx, hit := range hits {
			entry, ok := fused[hit.Source.MemoryID]
			if !ok {
				entry = &fusedEntry{hit: hit}
				fused[hit.Source.MemoryID] = entry
			}
			entry.score += 1.0 / float64(rrfK+idx+1)
		}
	}

	result := make([]memoryRetrievalHit, 0, len(fused))
	for _, entry := range fused {
		hit := entry.hit
		hit.Score = entry.score
		result = append(result, hit)
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score == result[j].Score {
			return result[i].Source.CreatedAt.After(result[j].Source.CreatedAt)
		}
		return result[i].Score > result[j].Score
	})
	return result
}

// formatWorkingSnapshot formats working snapshot.
func formatWorkingSnapshot(snapshot *model.WorkingMemorySnapshot) string {
	var builder strings.Builder
	builder.WriteString("## Working Memory\n")
	builder.WriteString("Summary: ")
	builder.WriteString(strings.TrimSpace(snapshot.Summary))

	var facts []string
	_ = json.Unmarshal([]byte(snapshot.FactsJSON), &facts)
	if len(facts) > 0 {
		builder.WriteString("\nFacts:\n")
		for _, fact := range facts {
			builder.WriteString("- ")
			builder.WriteString(fact)
			builder.WriteString("\n")
		}
	}
	return strings.TrimSpace(builder.String())
}

// formatMemoryLabel formats memory label.
func formatMemoryLabel(doc model.MemoryEsDocument) string {
	datePart := ""
	if !doc.CreatedAt.IsZero() {
		datePart = doc.CreatedAt.Format("2006-01-02")
	}
	if datePart == "" {
		return fmt.Sprintf("memory:%s", doc.MemoryType)
	}
	return fmt.Sprintf("memory:%s@%s", doc.MemoryType, datePart)
}

// weightedContextScore handles weighted context score.
func weightedContextScore(item ContextSnippet) float64 {
	score := item.Score
	if item.SourceType == "memory" && !item.Timestamp.IsZero() {
		ageDays := math.Max(0, time.Since(item.Timestamp).Hours()/24)
		score += math.Max(0, 0.15-(ageDays/30.0)*0.15)
	}
	return score
}

// dedupeContextSnippets handles dedupe context snippets.
func dedupeContextSnippets(items []ContextSnippet) []ContextSnippet {
	seen := make(map[string]ContextSnippet, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		key := item.SourceType + ":" + item.ID
		existing, ok := seen[key]
		if !ok || item.Score > existing.Score {
			seen[key] = item
		}
	}

	result := make([]ContextSnippet, 0, len(seen))
	for _, item := range seen {
		result = append(result, item)
	}
	return result
}

// shouldRetrieveLongTermMemory reports whether retrieve long term memory.
func shouldRetrieveLongTermMemory(query string, plan model.AgentPlan, history []model.ChatMessage) bool {
	if len(history) == 0 {
		return false
	}
	if plan.SkipRetrieval {
		return false
	}
	if plan.NeedHistory {
		return true
	}
	q := strings.ToLower(strings.TrimSpace(query))
	triggers := []string{
		"上次", "之前", "刚才", "继续", "那个", "它", "还记得", "前面", "刚刚",
		"last time", "previous", "earlier", "continue", "that project", "remember",
	}
	for _, trigger := range triggers {
		if strings.Contains(q, trigger) {
			return true
		}
	}
	return false
}

// heuristicWorkingSummary handles heuristic working summary.
func heuristicWorkingSummary(history []model.ChatMessage, maxFacts int) *memorySummaryPayload {
	limited := limitHistoryMessages(history, 6)
	summary := truncateRunes(renderConversationForPrompt(limited), 320)

	facts := make([]string, 0, maxFacts)
	for _, msg := range limited {
		prefix := "User"
		if msg.Role == "assistant" {
			prefix = "Assistant"
		}
		facts = append(facts, truncateRunes(prefix+": "+msg.Content, 120))
		if len(facts) >= maxFacts {
			break
		}
	}

	combined := renderConversationForPrompt(limited)
	return &memorySummaryPayload{
		Summary:        summary,
		Facts:          facts,
		Entities:       extractEntitiesHeuristic(combined),
		ProfileUpdates: heuristicProfileUpdates(combined),
	}
}

// heuristicMemoryWritePayload handles heuristic memory write payload.
func heuristicMemoryWritePayload(question, answer string) *memoryWritePayload {
	combined := strings.TrimSpace(question + "\n" + answer)
	profileUpdates := heuristicProfileUpdates(combined)
	importance := 0.15
	memoryType := "fact"
	q := strings.ToLower(combined)

	if len(profileUpdates) > 0 {
		importance += 0.35
		memoryType = "preference"
	}
	if strings.Contains(q, "项目") || strings.Contains(q, "架构") || strings.Contains(q, "方案") || strings.Contains(q, "constraint") {
		importance += 0.25
		memoryType = "project"
	}
	if strings.Contains(q, "决定") || strings.Contains(q, "改成") || strings.Contains(q, "重构") || strings.Contains(q, "约束") {
		importance += 0.25
		memoryType = "decision"
	}
	if len([]rune(combined)) > 180 {
		importance += 0.1
	}
	importance = clampFloat(importance, 0, 0.95)

	return &memoryWritePayload{
		ShouldStore:    importance >= 0.45,
		MemoryType:     memoryType,
		Summary:        truncateRunes(combined, 240),
		Content:        truncateRunes(combined, 600),
		Entities:       extractEntitiesHeuristic(combined),
		Importance:     importance,
		ProfileUpdates: profileUpdates,
	}
}

// heuristicProfileUpdates handles heuristic profile updates.
func heuristicProfileUpdates(text string) []profileUpdateRecord {
	lower := strings.ToLower(text)
	var updates []profileUpdateRecord

	languages := map[string]string{
		" go ":        "Go",
		" golang ":    "Go",
		" rust ":      "Rust",
		" java ":      "Java",
		" python ":    "Python",
		" vue ":       "Vue",
		" react ":     "React",
		" typescript": "TypeScript",
	}
	padded := " " + lower + " "
	for key, value := range languages {
		if strings.Contains(padded, key) {
			updates = append(updates, profileUpdateRecord{
				SlotKey:    "primary_language",
				SlotValue:  value,
				Confidence: 0.72,
			})
			break
		}
	}

	if strings.Contains(lower, "简短") || strings.Contains(lower, "精简") || strings.Contains(lower, "concise") {
		updates = append(updates, profileUpdateRecord{
			SlotKey:    "preferred_answer_style",
			SlotValue:  "concise",
			Confidence: 0.68,
		})
	}
	if strings.Contains(lower, "详细") || strings.Contains(lower, "详细一点") || strings.Contains(lower, "detailed") {
		updates = append(updates, profileUpdateRecord{
			SlotKey:    "preferred_answer_style",
			SlotValue:  "detailed",
			Confidence: 0.68,
		})
	}

	return normalizeProfileUpdates(updates)
}

// extractEntitiesHeuristic extracts entities heuristic.
func extractEntitiesHeuristic(text string) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\b[A-Z][A-Za-z0-9\-_]{1,24}\b`),
		regexp.MustCompile(`\b[a-zA-Z0-9_\-]+\.(pdf|docx|md|txt|pptx|xlsx)\b`),
		regexp.MustCompile(`[\p{Han}]{2,8}(系统|项目|方案|服务|模型|知识库)`),
	}

	entities := make([]string, 0, 8)
	seen := make(map[string]struct{})
	for _, pattern := range patterns {
		for _, item := range pattern.FindAllString(text, -1) {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			entities = append(entities, item)
			if len(entities) >= 8 {
				return entities
			}
		}
	}
	return entities
}

// limitHistoryMessages limits history messages.
func limitHistoryMessages(history []model.ChatMessage, maxMessages int) []model.ChatMessage {
	if maxMessages <= 0 || len(history) <= maxMessages {
		return history
	}
	return history[len(history)-maxMessages:]
}

// renderConversationForPrompt renders conversation for prompt.
func renderConversationForPrompt(history []model.ChatMessage) string {
	var builder strings.Builder
	for _, msg := range history {
		role := strings.Title(strings.TrimSpace(msg.Role))
		if role == "" {
			role = "User"
		}
		builder.WriteString(role)
		builder.WriteString(": ")
		builder.WriteString(strings.TrimSpace(msg.Content))
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

// estimateTextTokens estimates text tokens.
func estimateTextTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}

	count := 0
	inWord := false
	for _, r := range text {
		switch {
		case isHanRune(r):
			count++
			inWord = false
		case isAlphaNumRune(r):
			if !inWord {
				count++
				inWord = true
			}
		default:
			inWord = false
		}
	}
	return count
}

// isHanRune reports whether han rune.
func isHanRune(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF)
}

// isAlphaNumRune reports whether alpha num rune.
func isAlphaNumRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// normalizeStringList normalizes string list.
func normalizeStringList(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		value := strings.TrimSpace(item)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// normalizeProfileUpdates normalizes profile updates.
func normalizeProfileUpdates(items []profileUpdateRecord) []profileUpdateRecord {
	result := make([]profileUpdateRecord, 0, len(items))
	for _, item := range items {
		key := normalizeProfileSlotKey(item.SlotKey)
		value := strings.TrimSpace(item.SlotValue)
		if key == "" || value == "" {
			continue
		}
		result = append(result, profileUpdateRecord{
			SlotKey:    key,
			SlotValue:  value,
			Confidence: clampFloat(item.Confidence, 0.1, 0.99),
		})
	}
	return result
}

// normalizeProfileSlotKey normalizes profile slot key.
func normalizeProfileSlotKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "language", "tech_stack", "coding_language", "preferred_language", "primary_language":
		return "primary_language"
	case "answer_style", "response_style", "preferred_answer_style":
		return "preferred_answer_style"
	case "project", "current_project":
		return "current_project"
	case "role":
		return "role"
	case "deployment_env", "environment":
		return "deployment_env"
	default:
		return ""
	}
}

// normalizeMemoryType normalizes memory type.
func normalizeMemoryType(memoryType string) string {
	value := strings.ToLower(strings.TrimSpace(memoryType))
	switch value {
	case "preference", "project", "decision", "constraint", "fact":
		return value
	default:
		return "fact"
	}
}

// clampFloat clamps float.
func clampFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

// truncateRunes truncates runes.
func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}
