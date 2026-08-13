// Package service contains business logic.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/internal/serverconfig"
	"code-agent/internal/telemetry/genai"
	"code-agent/pkg/embedding"
	"code-agent/pkg/log"
	"code-agent/pkg/reranker"

	"github.com/elastic/go-elasticsearch/v8"
)

var (
	quotedPhrasePatterns = []*regexp.Regexp{
		regexp.MustCompile(`"([^"]+)"`),
		regexp.MustCompile(`“([^”]+)”`),
		regexp.MustCompile(`'([^']+)'`),
		regexp.MustCompile(`‘([^’]+)’`),
	}
	normalizePunctPattern = regexp.MustCompile(`[^\p{Han}a-z0-9\s]+`)
	normalizeSpacePattern = regexp.MustCompile(`\s+`)
)

// SearchService defines search operations.
type SearchService interface {
	HybridSearch(ctx context.Context, query string, topK int, user *model.User) ([]model.SearchResponseDTO, error)
	Search(ctx context.Context, options SearchOptions, user *model.User) ([]model.SearchResponseDTO, error)
	SetTracer(t genai.Tracer)
}

// SearchOptions represents a search options.
type SearchOptions struct {
	Query         string
	TopK          int
	DisableRerank bool
	Mode          model.RetrievalMode
}

// searchService implements search operations.
type searchService struct {
	embeddingClient embedding.Client
	rerankerClient  reranker.Client
	esClient        *elasticsearch.Client
	userService     UserService
	uploadRepo      repository.UploadRepository
	indexName       string
	retrievalCfg    serverconfig.RetrievalConfig
	observer        *retrievalObserver
	embedderModel   string
	embedderDim     int
	tracer          genai.Tracer
}

// retrievalHit represents a retrieval hit.
type retrievalHit struct {
	ID     string
	Score  float64
	Source model.EsDocument
}

// esSearchResponse describes the ES search response payload.
type esSearchResponse struct {
	Hits struct {
		Hits []struct {
			ID     string           `json:"_id"`
			Score  float64          `json:"_score"`
			Source model.EsDocument `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// retrievalObservation represents a retrieval observation.
type retrievalObservation struct {
	RecallAt100      float64
	NDCGAt5          float64
	LatencyMs        float64
	BM25Candidates   int
	VectorCandidates int
	PhraseCandidates int
	FusedCandidates  int
	Returned         int
	RerankApplied    bool
	RerankTimeout    bool
}

// retrievalSnapshot captures the retrieval snapshot.
type retrievalSnapshot struct {
	LatencyP95Ms      float64
	RerankTimeoutRate float64
}

// retrievalObserver represents a retrieval observer.
type retrievalObserver struct {
	mu             sync.Mutex
	maxSamples     int
	latencySamples []float64
	totalRequests  int64
	timeoutCount   int64
}

// NewSearchService creates a search service.
func NewSearchService(
	embeddingClient embedding.Client,
	rerankerClient reranker.Client,
	esClient *elasticsearch.Client,
	userService UserService,
	uploadRepo repository.UploadRepository,
	indexName string,
	retrievalCfg serverconfig.RetrievalConfig,
	embedderModel string,
	embedderDim int,
) SearchService {
	if strings.TrimSpace(indexName) == "" {
		indexName = "knowledge_base"
	}
	return &searchService{
		embeddingClient: embeddingClient,
		rerankerClient:  rerankerClient,
		esClient:        esClient,
		userService:     userService,
		uploadRepo:      uploadRepo,
		indexName:       indexName,
		retrievalCfg:    normalizeRetrievalConfig(retrievalCfg),
		observer:        newRetrievalObserver(512),
		embedderModel:   embedderModel,
		embedderDim:     embedderDim,
	}
}

// HybridSearch handles hybrid search.
func (s *searchService) HybridSearch(ctx context.Context, query string, topK int, user *model.User) ([]model.SearchResponseDTO, error) {
	return s.Search(ctx, SearchOptions{
		Query: query,
		TopK:  topK,
		Mode:  model.RetrievalModeHybrid,
	}, user)
}

// SetTracer injects a genai.Tracer for creating embedding/rerank child spans.
func (s *searchService) SetTracer(t genai.Tracer) {
	s.tracer = t
}

// Search handles search.
func (s *searchService) Search(ctx context.Context, options SearchOptions, user *model.User) ([]model.SearchResponseDTO, error) {
	start := time.Now()
	query := strings.TrimSpace(options.Query)
	if query == "" {
		return []model.SearchResponseDTO{}, nil
	}
	if user == nil {
		return nil, errors.New("user is required")
	}

	requestedTopK := resolveRequestedTopK(options.TopK, s.retrievalCfg)
	mode := options.Mode
	switch mode {
	case model.RetrievalModeBM25, model.RetrievalModeVector, model.RetrievalModeHybrid:
	default:
		mode = model.RetrievalModeHybrid
	}

	rerankerEnabled := s.rerankerClient != nil && s.rerankerClient.Enabled() && !rerankDisabled(ctx) && !options.DisableRerank
	returnTopK := resolveReturnTopK(requestedTopK, s.retrievalCfg, rerankerEnabled)
	keywordQuery, rawPhrase := normalizeQuery(query)
	if strings.TrimSpace(keywordQuery) == "" {
		keywordQuery = query
	}

	orgTags, err := s.userService.GetUserEffectiveOrgTags(user)
	if err != nil {
		log.Warnf("[SearchService] failed to load effective org tags for user=%s: %v", user.Username, err)
		orgTags = []string{}
	}

	var (
		bm25Hits   []retrievalHit
		vectorHits []retrievalHit
		bm25Err    error
		vectorErr  error
	)
	var wg sync.WaitGroup
	switch mode {
	case model.RetrievalModeBM25:
		wg.Add(1)
		go func() {
			defer wg.Done()
			bm25Hits, bm25Err = s.bm25Search(ctx, keywordQuery, s.retrievalCfg.BM25TopN, user.ID, orgTags)
		}()
	case model.RetrievalModeVector:
		wg.Add(1)
		go func() {
			defer wg.Done()
			vectorHits, vectorErr = s.vectorSearch(ctx, query, s.retrievalCfg.VectorTopN, user.ID, orgTags)
		}()
	default:
		wg.Add(2)
		go func() {
			defer wg.Done()
			bm25Hits, bm25Err = s.bm25Search(ctx, keywordQuery, s.retrievalCfg.BM25TopN, user.ID, orgTags)
		}()

		go func() {
			defer wg.Done()
			vectorHits, vectorErr = s.vectorSearch(ctx, query, s.retrievalCfg.VectorTopN, user.ID, orgTags)
		}()
	}

	wg.Wait()

	if mode == model.RetrievalModeVector && len(vectorHits) == 0 && vectorErr != nil {
		if isVectorDimensionMismatchError(vectorErr) && s.retrievalCfg.StrictMode {
			// Acceptance mode: dimension mismatch must fail explicitly,
			// never silently degrade to BM25 (design spec §10).
			return nil, fmt.Errorf("vector recall failed: %w", vectorErr)
		}
		log.Warnf("[SearchService] vector-only mode degraded to bm25 for query=%q: %v", query, vectorErr)
		bm25Hits, bm25Err = s.bm25Search(ctx, keywordQuery, s.retrievalCfg.BM25TopN, user.ID, orgTags)
		mode = model.RetrievalModeBM25
	}
	if mode == model.RetrievalModeBM25 && len(bm25Hits) == 0 && bm25Err != nil {
		log.Warnf("[SearchService] bm25-only mode degraded to vector for query=%q: %v", query, bm25Err)
		vectorHits, vectorErr = s.vectorSearch(ctx, query, s.retrievalCfg.VectorTopN, user.ID, orgTags)
		mode = model.RetrievalModeVector
	}

	if bm25Err != nil {
		log.Warnf("[SearchService] bm25 recall degraded for query=%q: %v", query, bm25Err)
	}
	if vectorErr != nil {
		if isVectorDimensionMismatchError(vectorErr) {
			log.Warnf("[SearchService] vector recall skipped due to dimension mismatch, query=%q: %v", query, vectorErr)
		} else {
			log.Warnf("[SearchService] vector recall degraded for query=%q: %v", query, vectorErr)
		}
	}
	if len(bm25Hits) == 0 && len(vectorHits) == 0 && bm25Err != nil && vectorErr != nil {
		return nil, fmt.Errorf("bm25 and vector recall both failed: bm25=%v vector=%v", bm25Err, vectorErr)
	}

	phraseHits := []retrievalHit{}
	if mode != model.RetrievalModeVector && shouldTriggerPhraseFallback(rawPhrase, len(bm25Hits), len(vectorHits), phraseFallbackThreshold(requestedTopK)) {
		phraseHits, err = s.phraseSearch(ctx, rawPhrase, s.retrievalCfg.BM25TopN, user.ID, orgTags)
		if err != nil {
			log.Warnf("[SearchService] phrase fallback degraded for query=%q phrase=%q: %v", query, rawPhrase, err)
			phraseHits = []retrievalHit{}
		}
	}

	fusedHits := fuseHitsByMode(mode, s.retrievalCfg.RRFK, bm25Hits, vectorHits, phraseHits)
	if len(fusedHits) == 0 {
		s.logRetrievalMetrics(query, keywordQuery, rawPhrase, retrievalObservation{
			RecallAt100:      -1,
			NDCGAt5:          -1,
			LatencyMs:        time.Since(start).Seconds() * 1000,
			BM25Candidates:   len(bm25Hits),
			VectorCandidates: len(vectorHits),
			PhraseCandidates: len(phraseHits),
			FusedCandidates:  0,
			Returned:         0,
		})
		return []model.SearchResponseDTO{}, nil
	}

	// Diversity cap feeds the reranker (RerankTopN) or the final topK when
	// reranking is disabled; truncation to returnTopK happens AFTER rerank so
	// the reranker sees a real candidate pool, not an already-truncated one.
	diversityCap := returnTopK
	if rerankerEnabled {
		diversityCap = maxInt(s.retrievalCfg.RerankTopN, returnTopK)
	}
	fusedHits = fuseAndExpand(fusedHits, diversityCap)
	finalHits := truncateHits(fusedHits, returnTopK)
	rerankApplied := false
	rerankTimeout := false
	if rerankerEnabled {
		finalHits, rerankApplied, rerankTimeout = s.rerankHits(ctx, query, fusedHits, returnTopK)
	}

	results, err := s.buildResponseDTOs(finalHits)
	if err != nil {
		return nil, err
	}

	s.logRetrievalMetrics(query, keywordQuery, rawPhrase, retrievalObservation{
		RecallAt100:      -1,
		NDCGAt5:          -1,
		LatencyMs:        time.Since(start).Seconds() * 1000,
		BM25Candidates:   len(bm25Hits),
		VectorCandidates: len(vectorHits),
		PhraseCandidates: len(phraseHits),
		FusedCandidates:  len(fusedHits),
		Returned:         len(results),
		RerankApplied:    rerankApplied,
		RerankTimeout:    rerankTimeout,
	})

	return results, nil
}

// bm25Search handles bm 25 search.
func (s *searchService) bm25Search(ctx context.Context, query string, topN int, userID uint, orgTags []string) ([]retrievalHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []retrievalHit{}, nil
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
					buildPermissionFilter(userID, orgTags),
				},
			},
		},
		"rescore": map[string]any{
			"window_size": topN,
			"query": map[string]any{
				"rescore_query": map[string]any{
					"match": map[string]any{
						"text_content": map[string]any{
							"query":    query,
							"operator": "and",
						},
					},
				},
				"query_weight":         0.2,
				"rescore_query_weight": 1.0,
			},
		},
		"size":             topN,
		"track_total_hits": false,
		"_source":          buildSourceFields(),
	}

	return s.searchOnce(ctx, body)
}

// vectorSearch handles vector search.
func (s *searchService) vectorSearch(ctx context.Context, query string, topN int, userID uint, orgTags []string) ([]retrievalHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []retrievalHit{}, nil
	}

	// Create an embedding span (child of the caller's retrieve span)
	var embSpan genai.Span
	if s.tracer != nil {
		ctx, embSpan = s.tracer.StartSpan(ctx, "embedding vectorSearch", genai.OperationEmbedding, genai.SystemGenAI)
		if s.embedderModel != "" {
			embSpan.SetAttributes(genai.RequestModelKV(s.embedderModel))
		}
		if s.embedderDim > 0 {
			embSpan.SetAttributes(genai.DocumentLengthKV(s.embedderDim))
		}
		embSpan.SetAttributes(genai.QueryHashKV(genai.HashQuery(query)))
		embSpan.SetAttributes(genai.EvalJoinAttributes(ctx)...)
		defer embSpan.End()
	}

	queryVector, err := s.embeddingClient.CreateEmbedding(ctx, query)
	if err != nil {
		if embSpan != nil {
			embSpan.RecordError(err)
		}
		return nil, fmt.Errorf("create query embedding failed: %w", err)
	}

	body := map[string]any{
		"knn": map[string]any{
			"field":          "vector",
			"query_vector":   queryVector,
			"k":              topN,
			"num_candidates": maxInt(topN*4, topN),
			"filter":         buildPermissionFilter(userID, orgTags),
		},
		"size":             topN,
		"track_total_hits": false,
		"_source":          buildSourceFields(),
	}

	return s.searchOnce(ctx, body)
}

// phraseSearch handles phrase search.
func (s *searchService) phraseSearch(ctx context.Context, phrase string, topN int, userID uint, orgTags []string) ([]retrievalHit, error) {
	phrase = strings.TrimSpace(phrase)
	if phrase == "" {
		return []retrievalHit{}, nil
	}

	body := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"must": []any{
					map[string]any{
						"match_phrase": map[string]any{
							"text_content": map[string]any{
								"query": phrase,
								"boost": 3.0,
							},
						},
					},
				},
				"filter": []any{
					buildPermissionFilter(userID, orgTags),
				},
			},
		},
		"size":             topN,
		"track_total_hits": false,
		"_source":          buildSourceFields(),
	}

	return s.searchOnce(ctx, body)
}

// searchOnce searches once.
func (s *searchService) searchOnce(ctx context.Context, body map[string]any) ([]retrievalHit, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, fmt.Errorf("encode elasticsearch query failed: %w", err)
	}

	res, err := s.esClient.Search(
		s.esClient.Search.WithContext(ctx),
		s.esClient.Search.WithIndex(s.indexName),
		s.esClient.Search.WithBody(&buf),
	)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch search failed: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		raw, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("elasticsearch returned status=%s body=%s", res.Status(), strings.TrimSpace(string(raw)))
	}

	var parsed esSearchResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode elasticsearch response failed: %w", err)
	}

	hits := make([]retrievalHit, 0, len(parsed.Hits.Hits))
	for _, hit := range parsed.Hits.Hits {
		hits = append(hits, retrievalHit{
			ID:     hit.ID,
			Score:  hit.Score,
			Source: hit.Source,
		})
	}
	return hits, nil
}

// rerankHits handles rerank hits.
func (s *searchService) rerankHits(ctx context.Context, query string, fusedHits []retrievalHit, returnTopK int) ([]retrievalHit, bool, bool) {
	if len(fusedHits) == 0 || returnTopK <= 0 {
		return []retrievalHit{}, false, false
	}

	candidates := truncateHits(fusedHits, s.retrievalCfg.RerankTopN)
	if len(candidates) == 0 {
		return truncateHits(fusedHits, returnTopK), false, false
	}

	docs := make([]reranker.Document, 0, len(candidates))
	for _, hit := range candidates {
		docs = append(docs, reranker.Document{
			ID:   candidateKey(hit.Source, hit.ID),
			Text: hit.Source.TextContent,
		})
	}

	// The reranker is an external model call.  Keep its trace payload
	// privacy-safe: only the query hash and candidate count leave this process.
	var rerankSpan genai.Span
	if s.tracer != nil {
		ctx, rerankSpan = s.tracer.StartSpan(ctx, "rerank rerankHits", genai.OperationRerank, genai.SystemGenAI)
		rerankSpan.SetAttributes(
			genai.QueryHashKV(genai.HashQuery(query)),
			genai.DocumentLengthKV(len(docs)),
		)
		rerankSpan.SetAttributes(genai.EvalJoinAttributes(ctx)...)
		defer rerankSpan.End()
	}

	timeout := time.Duration(s.retrievalCfg.RerankTimeoutMs) * time.Millisecond
	rerankCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results, err := s.rerankerClient.Rerank(rerankCtx, query, docs, returnTopK)
	if err != nil {
		if rerankSpan != nil {
			rerankSpan.RecordError(err)
		}
		timeoutHit := isTimeoutError(err) || errors.Is(rerankCtx.Err(), context.DeadlineExceeded)
		log.Warnf("[SearchService] rerank degraded for query=%q timeout=%t: %v", query, timeoutHit, err)
		return truncateHits(fusedHits, returnTopK), false, timeoutHit
	}
	MarkRerankApplied(ctx)

	reranked := make([]retrievalHit, 0, minInt(returnTopK, len(candidates)))
	seen := make(map[int]struct{}, len(results))
	for _, item := range results {
		if item.Index < 0 || item.Index >= len(candidates) {
			continue
		}
		if _, ok := seen[item.Index]; ok {
			continue
		}
		hit := candidates[item.Index]
		hit.Score = item.Score
		reranked = append(reranked, hit)
		seen[item.Index] = struct{}{}
		if len(reranked) >= returnTopK {
			break
		}
	}

	for idx, hit := range candidates {
		if len(reranked) >= returnTopK {
			break
		}
		if _, ok := seen[idx]; ok {
			continue
		}
		reranked = append(reranked, hit)
	}

	return reranked, len(reranked) > 0, false
}

// buildResponseDTOs builds response dt os.
func (s *searchService) buildResponseDTOs(hits []retrievalHit) ([]model.SearchResponseDTO, error) {
	md5Set := make(map[string]struct{}, len(hits))
	md5s := make([]string, 0, len(hits))
	for _, hit := range hits {
		if hit.Source.FileMD5 == "" {
			continue
		}
		if _, ok := md5Set[hit.Source.FileMD5]; ok {
			continue
		}
		md5Set[hit.Source.FileMD5] = struct{}{}
		md5s = append(md5s, hit.Source.FileMD5)
	}

	fileInfos, err := s.uploadRepo.FindBatchByMD5s(md5s)
	if err != nil {
		return nil, fmt.Errorf("batch load file info failed: %w", err)
	}

	fileNameMap := make(map[string]string, len(fileInfos))
	for _, info := range fileInfos {
		fileNameMap[info.FileMD5] = info.FileName
	}

	results := make([]model.SearchResponseDTO, 0, len(hits))
	for _, hit := range hits {
		fileName := fileNameMap[hit.Source.FileMD5]
		if fileName == "" {
			fileName = "unknown"
		}
		results = append(results, model.SearchResponseDTO{
			FileMD5:       hit.Source.FileMD5,
			FileName:      fileName,
			ChunkID:       hit.Source.ChunkID,
			TextContent:   hit.Source.TextContent,
			Score:         hit.Score,
			UserID:        strconv.FormatUint(uint64(hit.Source.UserID), 10),
			OrgTag:        hit.Source.OrgTag,
			IsPublic:      hit.Source.IsPublic,
			DocumentID:    hit.Source.DocumentID,
			ParentChunkID: hit.Source.ParentChunkID,
			SectionPath:   hit.Source.SectionPath,
			PageID:        hit.Source.PageID,
			PageSpan:      hit.Source.PageSpan,
			ElementIDs:    hit.Source.ElementIDs,
			ElementTypes:  hit.Source.ElementTypes,
			BBoxRefs:      hit.Source.BBoxRefs,
			AssetRefs:     hit.Source.AssetRefs,
			SourceURL:     hit.Source.SourceURL,
			TokenCount:    hit.Source.TokenCount,
		})
	}

	return results, nil
}

// logRetrievalMetrics handles log retrieval metrics.
func (s *searchService) logRetrievalMetrics(query, normalizedQuery, rawPhrase string, obs retrievalObservation) {
	snapshot := s.observer.Record(obs)
	log.Infow("[SearchService] retrieval metrics",
		"query", query,
		"normalizedQuery", normalizedQuery,
		"rawPhrase", rawPhrase,
		"recallAt100", obs.RecallAt100,
		"nDCGAt5", obs.NDCGAt5,
		"bm25Candidates", obs.BM25Candidates,
		"vectorCandidates", obs.VectorCandidates,
		"phraseCandidates", obs.PhraseCandidates,
		"fusedCandidates", obs.FusedCandidates,
		"returned", obs.Returned,
		"latencyMs", obs.LatencyMs,
		"latencyP95Ms", snapshot.LatencyP95Ms,
		"rerankApplied", obs.RerankApplied,
		"rerankTimeout", obs.RerankTimeout,
		"rerankTimeoutRate", snapshot.RerankTimeoutRate,
	)
}

// normalizeRetrievalConfig normalizes retrieval config.
func normalizeRetrievalConfig(cfg serverconfig.RetrievalConfig) serverconfig.RetrievalConfig {
	if cfg.BM25TopN <= 0 {
		cfg.BM25TopN = 100
	}
	if cfg.VectorTopN <= 0 {
		cfg.VectorTopN = 100
	}
	if cfg.RRFK <= 0 {
		cfg.RRFK = 60
	}
	if cfg.RerankTopN <= 0 {
		cfg.RerankTopN = 100
	}
	if cfg.FinalTopK <= 0 {
		cfg.FinalTopK = 5
	}
	if cfg.RerankTimeoutMs <= 0 {
		cfg.RerankTimeoutMs = 120
	}
	if cfg.EvidenceTokenBudget <= 0 {
		cfg.EvidenceTokenBudget = 4096
	}
	return cfg
}

// resolveRequestedTopK resolves requested top k.
func resolveRequestedTopK(topK int, cfg serverconfig.RetrievalConfig) int {
	if topK <= 0 {
		topK = cfg.FinalTopK
	}
	maxAllowed := maxInt(cfg.BM25TopN, maxInt(cfg.VectorTopN, maxInt(cfg.RerankTopN, cfg.FinalTopK)))
	if topK > maxAllowed {
		topK = maxAllowed
	}
	if topK <= 0 {
		return 1
	}
	return topK
}

// resolveReturnTopK resolves return top k.
func resolveReturnTopK(requestedTopK int, cfg serverconfig.RetrievalConfig, rerankerEnabled bool) int {
	if requestedTopK <= 0 {
		requestedTopK = cfg.FinalTopK
	}
	if rerankerEnabled {
		return minInt(requestedTopK, cfg.FinalTopK)
	}
	return requestedTopK
}

// buildPermissionFilter builds permission filter.
func buildPermissionFilter(userID uint, orgTags []string) map[string]any {
	should := []any{
		map[string]any{"term": map[string]any{"user_id": userID}},
		map[string]any{"term": map[string]any{"is_public": true}},
	}
	if len(orgTags) > 0 {
		should = append(should, map[string]any{"terms": map[string]any{"org_tag": orgTags}})
	}
	return map[string]any{
		"bool": map[string]any{
			"should":               should,
			"minimum_should_match": 1,
		},
	}
}

// buildSourceFields builds source fields.
func buildSourceFields() []string {
	return []string{"file_md5", "chunk_id", "text_content", "user_id", "org_tag", "is_public", "document_id", "parent_chunk_id", "section_path", "page_id", "page_span", "element_ids", "element_types", "bbox_refs", "asset_refs", "source_url", "token_count"}
}

// fuseAndExpand keeps evidence diverse by allowing only one hit per
// document+page (or document+parent) group. PageIDs are document-local, so
// the dedup key must include the document ID — two different documents with
// the same page number must both survive.
func fuseAndExpand(hits []retrievalHit, topK int) []retrievalHit {
	if topK <= 0 {
		return []retrievalHit{}
	}
	out := make([]retrievalHit, 0, minInt(len(hits), topK))
	seen := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		key := hit.Source.DocumentID + "/" + hit.Source.PageID
		if hit.Source.PageID == "" {
			key = hit.Source.DocumentID + "/" + hit.Source.ParentChunkID
		}
		if key == "/" {
			key = candidateKey(hit.Source, hit.ID)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, hit)
		if len(out) >= topK {
			break
		}
	}
	return out
}

// fuseHitsByMode fuses hits by mode.
func fuseHitsByMode(mode model.RetrievalMode, rrfK int, bm25Hits, vectorHits, phraseHits []retrievalHit) []retrievalHit {
	switch mode {
	case model.RetrievalModeBM25:
		return rrfFuse(rrfK, bm25Hits, phraseHits)
	case model.RetrievalModeVector:
		return truncateHits(vectorHits, len(vectorHits))
	default:
		return rrfFuse(rrfK, bm25Hits, vectorHits, phraseHits)
	}
}

// rrfFuse handles rrf fuse.
func rrfFuse(rrfK int, hitLists ...[]retrievalHit) []retrievalHit {
	type fusedEntry struct {
		hit   retrievalHit
		score float64
	}

	fused := make(map[string]*fusedEntry)
	for _, hits := range hitLists {
		for idx, hit := range hits {
			key := candidateKey(hit.Source, hit.ID)
			entry, ok := fused[key]
			if !ok {
				entry = &fusedEntry{hit: hit}
				fused[key] = entry
			}
			entry.score += 1.0 / float64(rrfK+idx+1)
		}
	}

	out := make([]retrievalHit, 0, len(fused))
	for _, entry := range fused {
		hit := entry.hit
		hit.Score = entry.score
		out = append(out, hit)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return candidateKey(out[i].Source, out[i].ID) < candidateKey(out[j].Source, out[j].ID)
		}
		return out[i].Score > out[j].Score
	})
	return out
}

// truncateHits truncates hits.
func truncateHits(hits []retrievalHit, topN int) []retrievalHit {
	if topN <= 0 || len(hits) <= topN {
		out := make([]retrievalHit, len(hits))
		copy(out, hits)
		return out
	}
	out := make([]retrievalHit, topN)
	copy(out, hits[:topN])
	return out
}

// candidateKey handles candidate key.
func candidateKey(doc model.EsDocument, fallbackID string) string {
	if strings.TrimSpace(doc.FileMD5) != "" {
		return fmt.Sprintf("%s:%d", doc.FileMD5, doc.ChunkID)
	}
	return fallbackID
}

// normalizeQuery normalizes query.
func normalizeQuery(q string) (string, string) {
	raw := strings.TrimSpace(q)
	if raw == "" {
		return "", ""
	}

	rawPhrase := extractQuotedPhrase(raw)
	if rawPhrase == "" {
		rawPhrase = raw
	}

	normalized := strings.ToLower(raw)
	stopPhrases := []string{
		"请问", "请帮我", "帮我", "告诉我", "介绍一下", "是什么", "是啥", "是谁",
		"怎么", "如何", "有没有", "一下", "一下子", "呢", "吗",
	}
	for _, phrase := range stopPhrases {
		normalized = strings.ReplaceAll(normalized, phrase, " ")
	}
	normalized = normalizePunctPattern.ReplaceAllString(normalized, " ")
	normalized = normalizeSpacePattern.ReplaceAllString(normalized, " ")
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		normalized = strings.TrimSpace(rawPhrase)
	}

	return normalized, strings.TrimSpace(rawPhrase)
}

// extractQuotedPhrase extracts quoted phrase.
func extractQuotedPhrase(q string) string {
	for _, pattern := range quotedPhrasePatterns {
		matches := pattern.FindStringSubmatch(q)
		if len(matches) > 1 {
			phrase := strings.TrimSpace(matches[1])
			if phrase != "" {
				return phrase
			}
		}
	}
	return ""
}

// shouldTriggerPhraseFallback reports whether trigger phrase fallback.
func shouldTriggerPhraseFallback(rawPhrase string, bm25Count, vectorCount, threshold int) bool {
	if strings.TrimSpace(rawPhrase) == "" {
		return false
	}
	return bm25Count < threshold || vectorCount < threshold
}

// phraseFallbackThreshold handles phrase fallback threshold.
func phraseFallbackThreshold(requestedTopK int) int {
	return minInt(maxInt(requestedTopK, 5), 20)
}

// isVectorDimensionMismatchError reports whether vector dimension mismatch error.
func isVectorDimensionMismatchError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "dimension") ||
		strings.Contains(msg, "dimensions") ||
		strings.Contains(msg, "different than the query") ||
		strings.Contains(msg, "vector dims") ||
		strings.Contains(msg, "query_vector")
}

// isTimeoutError reports whether timeout error.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded")
}

// newRetrievalObserver creates a retrieval observer.
func newRetrievalObserver(maxSamples int) *retrievalObserver {
	if maxSamples <= 0 {
		maxSamples = 512
	}
	return &retrievalObserver{
		maxSamples:     maxSamples,
		latencySamples: make([]float64, 0, maxSamples),
	}
}

// Record handles record.
func (o *retrievalObserver) Record(obs retrievalObservation) retrievalSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.totalRequests++
	if obs.RerankTimeout {
		o.timeoutCount++
	}
	o.latencySamples = append(o.latencySamples, obs.LatencyMs)
	if len(o.latencySamples) > o.maxSamples {
		o.latencySamples = append([]float64{}, o.latencySamples[len(o.latencySamples)-o.maxSamples:]...)
	}

	sorted := append([]float64{}, o.latencySamples...)
	sort.Float64s(sorted)

	timeoutRate := 0.0
	if o.totalRequests > 0 {
		timeoutRate = float64(o.timeoutCount) / float64(o.totalRequests)
	}

	return retrievalSnapshot{
		LatencyP95Ms:      percentile(sorted, 0.95),
		RerankTimeoutRate: timeoutRate,
	}
}

// percentile returns the requested percentile from a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// minInt returns the smaller of two integers.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// maxInt returns the larger of two integers.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
