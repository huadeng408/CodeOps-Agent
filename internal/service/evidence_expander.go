package service

import (
	"context"
	"fmt"
	"strings"

	"code-agent/internal/model"
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
