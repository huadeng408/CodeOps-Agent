package service

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/internal/telemetry/genai"
	"code-agent/pkg/log"
)

// TestMain initializes the zap logger so degraded-path tests can log warnings.
func TestMain(m *testing.M) {
	log.Init("error", "console", "")
	os.Exit(m.Run())
}

// stubSearchService returns fixed retrieval hits for wiring tests.
type stubSearchService struct {
	results []model.SearchResponseDTO
	err     error
}

func (s *stubSearchService) HybridSearch(ctx context.Context, query string, topK int, user *model.User) ([]model.SearchResponseDTO, error) {
	return s.Search(ctx, SearchOptions{Query: query, TopK: topK, Mode: model.RetrievalModeHybrid}, user)
}

func (s *stubSearchService) Search(_ context.Context, _ SearchOptions, _ *model.User) ([]model.SearchResponseDTO, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.results, nil
}

func (s *stubSearchService) SetTracer(_ genai.Tracer) {}

// failingEvidenceLoader simulates a down vector store during expansion.
type failingEvidenceLoader struct{}

func (failingEvidenceLoader) FindByParentChunkID(string) ([]*model.DocumentVector, error) {
	return nil, errors.New("vector store unavailable")
}

// evHitDTO builds a search hit matching the store fixtures (md5-evidence).
func evHitDTO(chunkID int, parent, text string, userID uint, orgTag string, tokens int) model.SearchResponseDTO {
	return model.SearchResponseDTO{
		FileMD5:       "md5-evidence",
		FileName:      "evidence.pdf",
		ChunkID:       chunkID,
		TextContent:   text,
		Score:         1.0,
		UserID:        strconv.FormatUint(uint64(userID), 10),
		OrgTag:        orgTag,
		DocumentID:    "doc-evidence",
		ParentChunkID: parent,
		PageID:        "p1",
		ElementIDs:    []string{"e1"},
		TokenCount:    tokens,
	}
}

// evStoreVector builds a store fixture matching evHitDTO's file identity.
func evStoreVector(chunkID int, parent string, userID uint, orgTag, text string, tokens int) *model.DocumentVector {
	v := evVector(chunkID, parent, "p1", userID, orgTag, text, tokens)
	v.FileMD5 = "md5-evidence"
	return v
}

func newChatWithEvidence(search SearchService, loader EvidenceLoader, userService UserService) *chatService {
	return &chatService{
		searchService:  search,
		evidenceLoader: loader,
		userService:    userService,
	}
}

// withEvidenceBudget sets the expansion token budget for one test.
func withEvidenceBudget(t *testing.T, budget int) func() {
	t.Helper()
	old := serverconfig.Conf.Retrieval.EvidenceTokenBudget
	serverconfig.Conf.Retrieval.EvidenceTokenBudget = budget
	return func() { serverconfig.Conf.Retrieval.EvidenceTokenBudget = old }
}

func TestChatRetrieveKnowledgeExpandsNeighborEvidence(t *testing.T) {
	store := &fakeVectorStore{vectors: []*model.DocumentVector{
		evStoreVector(1, "parent-1", 7, "research", "child text", 20),
		evStoreVector(2, "parent-1", 7, "research", "neighbor text", 30),
	}}
	search := &stubSearchService{results: []model.SearchResponseDTO{
		evHitDTO(1, "parent-1", "child text", 7, "research", 20),
	}}
	chat := newChatWithEvidence(search, store, &stubUserService{})

	plan := model.DefaultAgentPlan("证据扩展")
	items, err := chat.retrieveKnowledgeWithPlan(context.Background(), "什么是证据扩展", plan, &model.User{ID: 7, Username: "u7", OrgTags: "research"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected hit + neighbor evidence, got %d items: %#v", len(items), items)
	}
	foundNeighbor := false
	for _, item := range items {
		if item.Text == "neighbor text" {
			foundNeighbor = true
		}
	}
	if !foundNeighbor {
		t.Fatalf("neighbor evidence missing from chat context: %#v", items)
	}
}

func TestChatRetrieveKnowledgeDegradesOnEvidenceLoaderFailure(t *testing.T) {
	search := &stubSearchService{results: []model.SearchResponseDTO{
		evHitDTO(1, "parent-1", "child text", 7, "research", 20),
	}}
	chat := newChatWithEvidence(search, failingEvidenceLoader{}, &stubUserService{})

	plan := model.DefaultAgentPlan("证据扩展")
	items, err := chat.retrieveKnowledgeWithPlan(context.Background(), "什么是证据扩展", plan, &model.User{ID: 7, Username: "u7", OrgTags: "research"})
	if err != nil {
		t.Fatalf("evidence loader failure must degrade, not fail retrieval: %v", err)
	}
	if len(items) != 1 || items[0].Text != "child text" {
		t.Fatalf("expected unexpanded hit only, got %#v", items)
	}
}

func TestOrchestratorSearchKnowledgeExpandsNeighborEvidence(t *testing.T) {
	store := &fakeVectorStore{vectors: []*model.DocumentVector{
		evStoreVector(1, "parent-1", 7, "research", "child text", 20),
		evStoreVector(2, "parent-1", 7, "research", "neighbor text", 30),
	}}
	search := &stubSearchService{results: []model.SearchResponseDTO{
		evHitDTO(1, "parent-1", "child text", 7, "research", 20),
	}}
	svc := NewOrchestratorSupportService(search, nil, nil, nil, store, &stubUserService{})

	resp, err := svc.SearchKnowledge(context.Background(), &model.OrchestratorKnowledgeSearchRequest{
		User:  model.NewOrchestratorUser(&model.User{ID: 7, Username: "u7", OrgTags: "research"}),
		Query: "evidence query",
		TopK:  5,
		Mode:  model.RetrievalModeHybrid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected hit + neighbor evidence, got %d results: %#v", len(resp.Results), resp.Results)
	}
	var neighbor *model.SearchResponseDTO
	for i := range resp.Results {
		if resp.Results[i].TextContent == "neighbor text" {
			neighbor = &resp.Results[i]
		}
		if resp.Results[i].ExpansionStatus != "full" {
			t.Fatalf("expected full expansion status, got %q", resp.Results[i].ExpansionStatus)
		}
	}
	if neighbor == nil {
		t.Fatalf("neighbor evidence missing from search knowledge results: %#v", resp.Results)
	}
	if neighbor.CitationKey == "" {
		t.Fatalf("neighbor evidence must carry a citation key: %#v", neighbor)
	}
	if neighbor.TokenCount != 30 {
		t.Fatalf("neighbor token count lost in expansion: %d", neighbor.TokenCount)
	}
}

func TestOrchestratorSearchKnowledgeFiltersCrossTenantNeighbor(t *testing.T) {
	store := &fakeVectorStore{vectors: []*model.DocumentVector{
		evStoreVector(1, "parent-1", 7, "research", "child text", 20),
		evStoreVector(2, "parent-1", 999, "attacker-org", "other tenant text", 30),
	}}
	search := &stubSearchService{results: []model.SearchResponseDTO{
		evHitDTO(1, "parent-1", "child text", 7, "research", 20),
	}}
	svc := NewOrchestratorSupportService(search, nil, nil, nil, store, &stubUserService{})

	resp, err := svc.SearchKnowledge(context.Background(), &model.OrchestratorKnowledgeSearchRequest{
		User:  model.NewOrchestratorUser(&model.User{ID: 7, Username: "u7", OrgTags: "research"}),
		Query: "evidence query",
		TopK:  5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("cross-tenant neighbor leaked into results: %#v", resp.Results)
	}
	if resp.Results[0].TextContent == "other tenant text" {
		t.Fatalf("cross-tenant neighbor leaked: %#v", resp.Results[0])
	}
	if resp.Results[0].ExpansionStatus != "partial" {
		t.Fatalf("expected partial status when no neighbor is visible, got %q", resp.Results[0].ExpansionStatus)
	}
}

func TestOrchestratorSearchKnowledgeDegradesOnEvidenceLoaderFailure(t *testing.T) {
	search := &stubSearchService{results: []model.SearchResponseDTO{
		evHitDTO(1, "parent-1", "child text", 7, "research", 20),
	}}
	svc := NewOrchestratorSupportService(search, nil, nil, nil, failingEvidenceLoader{}, &stubUserService{})

	resp, err := svc.SearchKnowledge(context.Background(), &model.OrchestratorKnowledgeSearchRequest{
		User:  model.NewOrchestratorUser(&model.User{ID: 7, Username: "u7", OrgTags: "research"}),
		Query: "evidence query",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("evidence loader failure must degrade, not fail search: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].TextContent != "child text" {
		t.Fatalf("expected unexpanded hit only, got %#v", resp.Results)
	}
}

func TestExpandSearchResultsRespectsTokenBudget(t *testing.T) {
	restore := withEvidenceBudget(t, 80)
	defer restore()

	store := &fakeVectorStore{vectors: []*model.DocumentVector{
		evStoreVector(1, "parent-1", 7, "research", "child text", 50),
		evStoreVector(2, "parent-1", 7, "research", "sibling one", 60),
		evStoreVector(3, "parent-1", 7, "research", "sibling two", 60),
	}}
	expander := NewEvidenceExpander(store, 7, []string{"research"})

	hits := []model.SearchResponseDTO{evHitDTO(1, "parent-1", "child text", 7, "research", 50)}
	expanded, err := expander.ExpandSearchResults(context.Background(), hits, evidenceTokenBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(expanded) != 2 {
		t.Fatalf("expected hit + 1 neighbor within budget, got %d: %#v", len(expanded), expanded)
	}
	for _, dto := range expanded {
		if dto.TextContent == "sibling two" {
			t.Fatalf("second sibling exceeded token budget: %#v", dto)
		}
	}
}
