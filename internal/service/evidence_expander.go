package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/log"
)

// EvidenceLoader loads neighbor evidence for a document vector.
type EvidenceLoader interface {
	// FindByParentChunkID returns all children of a parent chunk.
	FindByParentChunkID(parentChunkID string) ([]*model.DocumentVector, error)
}

// ExpandedEvidence is one output of the evidence expander.
type ExpandedEvidence struct {
	Source          model.DocumentVector
	CitationKey     string
	ExpansionStatus string // "full" when neighbors loaded, "partial" when parent missing
}

// EvidenceExpander loads parent/neighbor evidence for retrieval hits so the
// generator sees the surrounding context, not just the matched child. ACL is
// enforced on every loaded neighbor: a neighbor from another tenant is never
// returned. Token budget truncates the expansion; missing parents yield
// partial status instead of fabricating success.
type EvidenceExpander struct {
	loader   EvidenceLoader
	userID   uint
	orgTags  []string
	maxTries int
}

// NewEvidenceExpander creates an expander bound to a user's ACL scope.
func NewEvidenceExpander(loader EvidenceLoader, userID uint, orgTags []string) *EvidenceExpander {
	return &EvidenceExpander{loader: loader, userID: userID, orgTags: orgTags, maxTries: 16}
}

// Expand returns, for each hit, the hit itself plus same-parent neighbors
// (excluding duplicates), deduplicated and capped by token budget. Each entry
// carries a stable citation key: source_uri + page + element_id when
// available, falling back to file_md5:chunk_id.
func (e *EvidenceExpander) Expand(ctx context.Context, hits []model.DocumentVector, tokenBudget int) ([]ExpandedEvidence, error) {
	if tokenBudget <= 0 {
		tokenBudget = 4096
	}
	out := make([]ExpandedEvidence, 0, len(hits))
	seen := make(map[string]struct{})
	usedTokens := 0
	dedup := make(map[string]struct{})

	for _, hit := range hits {
		if _, ok := seen[hit.FileMD5+":"+itoa(hit.ChunkID)]; ok {
			continue
		}
		neighborStatus := "partial"
		loadedNeighbors := make([]ExpandedEvidence, 0)
		if strings.TrimSpace(hit.ParentChunkID) != "" {
			neighbors, err := e.loader.FindByParentChunkID(hit.ParentChunkID)
			if err != nil {
				return nil, fmt.Errorf("expand parent %s: %w", hit.ParentChunkID, err)
			}
			loaded := 0
			for _, neighbor := range neighbors {
				// The parent lookup also returns the hit itself when its
				// parent_chunk_id matches; never treat self as evidence.
				if neighbor.FileMD5 == hit.FileMD5 && neighbor.ChunkID == hit.ChunkID {
					continue
				}
				if !e.aclAllows(neighbor) {
					continue
				}
				key := neighbor.FileMD5 + ":" + itoa(neighbor.ChunkID)
				if _, ok := dedup[key]; ok {
					continue
				}
				if usedTokens+neighbor.TokenCount > tokenBudget {
					continue
				}
				dedup[key] = struct{}{}
				usedTokens += neighbor.TokenCount
				seen[key] = struct{}{}
				loadedNeighbors = append(loadedNeighbors, ExpandedEvidence{
					Source:      *neighbor,
					CitationKey: citationKey(*neighbor),
				})
				loaded++
				if loaded >= e.maxTries {
					break
				}
			}
			if loaded > 0 {
				neighborStatus = "full"
			}
		}
		for i := range loadedNeighbors {
			loadedNeighbors[i].ExpansionStatus = neighborStatus
		}
		out = append(out, loadedNeighbors...)

		key := hit.FileMD5 + ":" + itoa(hit.ChunkID)
		if _, ok := dedup[key]; ok {
			continue
		}
		dedup[key] = struct{}{}
		usedTokens += hit.TokenCount
		status := "partial"
		if neighborStatus == "full" {
			status = "full"
		}
		out = append(out, ExpandedEvidence{
			Source:          hit,
			CitationKey:     citationKey(hit),
			ExpansionStatus: status,
		})
	}
	return out, nil
}

// ExpandSearchResults is the production entry point: it maps search DTO hits
// onto the expander's input shape, loads same-parent neighbor evidence, and
// maps the expanded evidence back into DTOs carrying token counts and
// citation metadata.
func (e *EvidenceExpander) ExpandSearchResults(ctx context.Context, hits []model.SearchResponseDTO, tokenBudget int) ([]model.SearchResponseDTO, error) {
	vectors := make([]model.DocumentVector, 0, len(hits))
	for i := range hits {
		vectors = append(vectors, searchDTOToDocumentVector(hits[i]))
	}
	expanded, err := e.Expand(ctx, vectors, tokenBudget)
	if err != nil {
		return nil, err
	}
	out := make([]model.SearchResponseDTO, 0, len(expanded))
	for _, ev := range expanded {
		out = append(out, expandedToSearchDTO(ev))
	}
	return out, nil
}

// searchDTOToDocumentVector maps a retrieval hit onto the expander's input
// shape. TokenCount is carried by the DTO (populated from the ES source);
// the string-encoded user id is parsed back for ACL bookkeeping.
func searchDTOToDocumentVector(dto model.SearchResponseDTO) model.DocumentVector {
	userID, err := strconv.ParseUint(dto.UserID, 10, 64)
	if err != nil {
		userID = 0
	}
	return model.DocumentVector{
		FileMD5:       dto.FileMD5,
		ChunkID:       dto.ChunkID,
		TextContent:   dto.TextContent,
		UserID:        uint(userID),
		OrgTag:        dto.OrgTag,
		IsPublic:      dto.IsPublic,
		DocumentID:    dto.DocumentID,
		ParentChunkID: dto.ParentChunkID,
		SectionPath:   dto.SectionPath,
		PageID:        dto.PageID,
		PageSpan:      dto.PageSpan,
		ElementIDs:    dto.ElementIDs,
		ElementTypes:  dto.ElementTypes,
		BBoxRefs:      dto.BBoxRefs,
		AssetRefs:     dto.AssetRefs,
		SourceURL:     dto.SourceURL,
		TokenCount:    dto.TokenCount,
	}
}

// expandedToSearchDTO maps one expanded evidence entry back into a search
// hit, preserving the citation key and expansion status for the generator.
func expandedToSearchDTO(ev ExpandedEvidence) model.SearchResponseDTO {
	v := ev.Source
	return model.SearchResponseDTO{
		FileMD5:         v.FileMD5,
		ChunkID:         v.ChunkID,
		TextContent:     v.TextContent,
		UserID:          strconv.FormatUint(uint64(v.UserID), 10),
		OrgTag:          v.OrgTag,
		IsPublic:        v.IsPublic,
		DocumentID:      v.DocumentID,
		ParentChunkID:   v.ParentChunkID,
		SectionPath:     v.SectionPath,
		PageID:          v.PageID,
		PageSpan:        v.PageSpan,
		ElementIDs:      v.ElementIDs,
		ElementTypes:    v.ElementTypes,
		BBoxRefs:        v.BBoxRefs,
		AssetRefs:       v.AssetRefs,
		SourceURL:       v.SourceURL,
		TokenCount:      v.TokenCount,
		CitationKey:     ev.CitationKey,
		ExpansionStatus: ev.ExpansionStatus,
	}
}

// effectiveOrgTagsForEvidence resolves the ACL scope the search used so
// neighbor evidence is judged against the same org tags. Failures degrade to
// the user's own scope (conservative: never broader than the search).
func effectiveOrgTagsForEvidence(userService UserService, user *model.User) []string {
	if userService == nil || user == nil {
		return []string{}
	}
	tags, err := userService.GetUserEffectiveOrgTags(user)
	if err != nil {
		log.Warnf("failed to load effective org tags for evidence expansion, user=%s: %v", user.Username, err)
		return []string{}
	}
	return tags
}

// evidenceTokenBudget returns the configured expansion budget; 0 falls back
// to the expander's built-in default.
func evidenceTokenBudget() int {
	return serverconfig.Conf.Retrieval.EvidenceTokenBudget
}

// expandEvidenceForUser is the shared wiring used by the chat and orchestrator
// support flows: it expands retrieval hits with same-parent neighbor evidence,
// degrading to the unexpanded hits when the loader fails so retrieval never
// dies because evidence expansion is unavailable.
func expandEvidenceForUser(ctx context.Context, loader EvidenceLoader, userService UserService, user *model.User, results []model.SearchResponseDTO, query string) []model.SearchResponseDTO {
	if loader == nil || user == nil || len(results) == 0 {
		return results
	}
	expander := NewEvidenceExpander(loader, user.ID, effectiveOrgTagsForEvidence(userService, user))
	expanded, err := expander.ExpandSearchResults(ctx, results, evidenceTokenBudget())
	if err != nil {
		log.Warnf("evidence expansion degraded for query=%q: %v", query, err)
		return results
	}
	return expanded
}

func (e *EvidenceExpander) aclAllows(v *model.DocumentVector) bool {
	if v.UserID == e.userID {
		return true
	}
	if v.IsPublic {
		return true
	}
	for _, tag := range e.orgTags {
		if v.OrgTag == tag {
			return true
		}
	}
	return false
}

// citationKey builds a stable citation for one chunk.
func citationKey(v model.DocumentVector) string {
	if v.DocumentID != "" && v.PageID != "" && len(v.ElementIDs) > 0 {
		return fmt.Sprintf("%s/%s/%s", v.DocumentID, v.PageID, v.ElementIDs[0])
	}
	return fmt.Sprintf("%s:%d", v.FileMD5, v.ChunkID)
}

// itoa renders a small non-negative integer without importing strconv in the
// hot path (used for chunk-id keys and citation fallbacks).
func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
