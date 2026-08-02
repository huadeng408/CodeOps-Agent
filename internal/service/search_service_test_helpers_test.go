package service

import (
	"context"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"

	"github.com/elastic/go-elasticsearch/v8"
)

// stubEmbedding returns a fixed 4-dim vector so vector searches are
// deterministic in tests.
type stubEmbedding struct{}

func (s *stubEmbedding) CreateEmbedding(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3, 0.4}, nil
}

func (s *stubEmbedding) CreateEmbeddings(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1, 0.2, 0.3, 0.4}
	}
	return out, nil
}

// stubUserService returns fixed effective org tags.
type stubUserService struct{}

func (s *stubUserService) Register(username, password string) (*model.User, error) {
	return nil, nil
}
func (s *stubUserService) Login(username, password string) (string, string, error) {
	return "", "", nil
}
func (s *stubUserService) GetProfile(username string) (*model.User, error) {
	return nil, nil
}
func (s *stubUserService) Logout(accessTokenString, refreshTokenString string) error {
	return nil
}
func (s *stubUserService) IsTokenBlacklisted(tokenString string) (bool, error) {
	return false, nil
}
func (s *stubUserService) SetUserPrimaryOrg(username, orgTag string) error {
	return nil
}
func (s *stubUserService) GetUserOrgTags(username string) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}
func (s *stubUserService) GetUserEffectiveOrgTags(user *model.User) ([]string, error) {
	return []string{"research"}, nil
}
func (s *stubUserService) RefreshToken(refreshTokenString string) (string, string, error) {
	return "", "", nil
}

// newESClientForURL builds an elasticsearch client pointed at an httptest URL.
func newESClientForURL(url string) (*elasticsearch.Client, error) {
	return elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{url},
	})
}

// newTestSearchService builds a search service wired to httptest servers.
func newTestSearchService(esURL string) SearchService {
	esClient, err := newESClientForURL(esURL)
	if err != nil {
		panic(err)
	}
	return NewSearchService(
		&stubEmbedding{},
		nil, // reranker disabled
		esClient,
		&stubUserService{},
		nil,
		"knowledge_base",
		normalizeRetrievalConfig(serverconfig.RetrievalConfig{
			BM25TopN: 100, VectorTopN: 100, RRFK: 60, FinalTopK: 20, RerankTopN: 100, StrictMode: true,
		}),
	)
}
