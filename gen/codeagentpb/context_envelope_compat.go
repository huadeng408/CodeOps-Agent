package codeagentpb

// GetP3Candidates is the source-compatible accessor for protocol revisions
// that called field 4 p3_candidates. The wire field and canonical accessor are
// P2; both names read the same single slice.
func (x *ContextEnvelope) GetP3Candidates() []string {
	if x != nil {
		return x.P2Candidates
	}
	return nil
}
