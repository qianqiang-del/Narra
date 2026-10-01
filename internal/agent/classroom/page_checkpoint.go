package classroom

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

const pageCheckpointVersion = 1

type pageCheckpoint struct {
	Version            int                   `json:"version"`
	InputHash          string                `json:"input_hash"`
	NextNode           string                `json:"next_node"`
	Plan               *PageExecutionPlan    `json:"plan,omitempty"`
	Evidence           *EvidenceBundle       `json:"evidence,omitempty"`
	Blocks             []contentBlock        `json:"blocks,omitempty"`
	Narration          []narrationSegment    `json:"narration,omitempty"`
	Review             *ReviewResult         `json:"review,omitempty"`
	ReviewArtifactHash string                `json:"review_artifact_hash,omitempty"`
	RevisionReview     *ReviewResult         `json:"revision_review,omitempty"`
	RevisionFallback   *reviewedPageSnapshot `json:"revision_fallback,omitempty"`
	HTML               string                `json:"html,omitempty"`
	ReviewNote         string                `json:"review_note,omitempty"`
	ResearchSteps      string                `json:"research_steps,omitempty"`
	ResearchNote       string                `json:"research_note,omitempty"`
	Revision           string                `json:"revision,omitempty"`
	Rounds             int                   `json:"rounds,omitempty"`
	BudgetUsed         int                   `json:"budget_used,omitempty"`
}

func pageCheckpointInputHash(state *pageRunState) string {
	raw, err := json.Marshal(struct {
		Version int         `json:"version"`
		Type    string      `json:"type"`
		Page    PlanPage    `json:"page"`
		Context PageContext `json:"context"`
	}{
		Version: pageCheckpointVersion,
		Type:    state.Scene.Type,
		Page:    state.Page,
		Context: state.Context,
	})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func checkpointFromState(state *pageRunState, nextNode string) pageCheckpoint {
	return pageCheckpoint{
		Version:            pageCheckpointVersion,
		InputHash:          pageCheckpointInputHash(state),
		NextNode:           nextNode,
		Plan:               state.Plan,
		Evidence:           state.Evidence,
		Blocks:             state.Blocks,
		Narration:          state.Narration,
		Review:             state.Review,
		ReviewArtifactHash: state.ReviewArtifactHash,
		RevisionReview:     state.RevisionReview,
		RevisionFallback:   state.RevisionFallback,
		HTML:               state.HTML,
		ReviewNote:         state.ReviewNote,
		ResearchSteps:      state.ResearchSteps,
		ResearchNote:       state.ResearchNote,
		Revision:           state.Revision,
		Rounds:             state.Rounds,
		BudgetUsed:         state.Budget.used,
	}
}

func restorePageCheckpoint(state *pageRunState, raw json.RawMessage) bool {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "{}" {
		return false
	}
	var checkpoint pageCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil ||
		checkpoint.Version != pageCheckpointVersion ||
		checkpoint.InputHash == "" ||
		checkpoint.InputHash != pageCheckpointInputHash(state) ||
		!validCheckpointNextNode(checkpoint.NextNode) {
		return false
	}
	state.Plan = checkpoint.Plan
	state.Evidence = checkpoint.Evidence
	state.Blocks = checkpoint.Blocks
	state.Narration = checkpoint.Narration
	state.Review = checkpoint.Review
	state.ReviewArtifactHash = checkpoint.ReviewArtifactHash
	state.RevisionReview = checkpoint.RevisionReview
	state.RevisionFallback = checkpoint.RevisionFallback
	state.HTML = checkpoint.HTML
	state.ReviewNote = checkpoint.ReviewNote
	state.ResearchSteps = checkpoint.ResearchSteps
	state.ResearchNote = checkpoint.ResearchNote
	state.Revision = checkpoint.Revision
	state.Rounds = checkpoint.Rounds
	state.Budget.used = checkpoint.BudgetUsed
	state.ResumeNode = checkpoint.NextNode
	return true
}

func validCheckpointNextNode(node string) bool {
	switch node {
	case pageNodePlan, pageNodeResearch, pageNodeContent, pageNodeNarration, pageNodeReview, pageNodeRevision, pageRouteDone:
		return true
	default:
		return false
	}
}

func (state *pageRunState) skipForResume(node string) bool {
	if state.ResumeNode == "" {
		return false
	}
	if state.ResumeNode == node {
		state.ResumeNode = ""
		return false
	}
	return true
}
