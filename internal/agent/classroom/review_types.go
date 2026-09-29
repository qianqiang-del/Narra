package classroom

import (
	"fmt"
	"strings"
)

// 审核问题的归属，决定修订路由把问题送回哪个节点。
const (
	reviewTargetPlan      = "plan"
	reviewTargetResearch  = "research"
	reviewTargetContent   = "content"
	reviewTargetNarration = "narration"
	reviewTargetBoth      = "both"
)

// 审核问题的严重程度，blocker 与 major 足以触发修订。
const (
	reviewSeverityMinor   = "minor"
	reviewSeverityMajor   = "major"
	reviewSeverityBlocker = "blocker"
)

// ReviewResult 是一次页面审核的结论。
type ReviewResult struct {
	Approved            bool          `json:"approved"`
	Score               int           `json:"score"`
	Issues              []ReviewIssue `json:"issues"`
	RevisionInstruction string        `json:"revision_instruction"`
}

// ReviewIssue 是审核发现的一个问题，Target 决定它回到哪个节点。
type ReviewIssue struct {
	Target   string `json:"target"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// pageReviewRecord 是落库的审核摘要：审核结论加实际消耗的修订轮次。
type pageReviewRecord struct {
	ReviewResult
	Rounds       int    `json:"rounds"`
	Note         string `json:"note,omitempty"`
	ResearchNote string `json:"research_note,omitempty"`
	ArtifactHash string `json:"artifact_hash,omitempty"`
}

// normalizeReview 归一化审核结论并就地丢弃无法定位的空问题。
func normalizeReview(review *ReviewResult) {
	if review.Score < 0 {
		review.Score = 0
	}
	if review.Score > 100 {
		review.Score = 100
	}
	review.RevisionInstruction = strings.TrimSpace(review.RevisionInstruction)

	issues := make([]ReviewIssue, 0, len(review.Issues))
	for _, issue := range review.Issues {
		issue.Message = strings.TrimSpace(issue.Message)
		if issue.Message == "" {
			continue
		}
		issue.Code = strings.TrimSpace(issue.Code)
		if !isReviewTarget(issue.Target) {
			issue.Target = reviewTargetContent
		}
		if !isReviewSeverity(issue.Severity) {
			issue.Severity = reviewSeverityMajor
		}
		issues = append(issues, issue)
	}
	review.Issues = issues

	// 判不通过却什么也指不出来，等于给不出可执行的修订；按通过处理，避免白跑一轮。
	if !review.Approved && len(issues) == 0 && review.RevisionInstruction == "" {
		review.Approved = true
	}
}

// revisionTarget 按问题清单归并出本轮修订的出口，优先级从做法到内容依次降低。
func revisionTarget(issues []ReviewIssue) string {
	targets := make(map[string]bool, len(issues))
	for _, issue := range issues {
		targets[issue.Target] = true
	}
	switch {
	case targets[reviewTargetPlan]:
		return reviewTargetPlan
	case targets[reviewTargetResearch]:
		return reviewTargetResearch
	case targets[reviewTargetContent] && targets[reviewTargetNarration]:
		return reviewTargetBoth
	case targets[reviewTargetContent]:
		return reviewTargetContent
	case targets[reviewTargetNarration]:
		return reviewTargetNarration
	default:
		return reviewTargetContent
	}
}

// reviewFeedback 把审核意见整理成给专家的修订指令。
func reviewFeedback(review *ReviewResult) string {
	var builder strings.Builder
	if instruction := strings.TrimSpace(review.RevisionInstruction); instruction != "" {
		builder.WriteString(instruction)
	}
	for _, issue := range review.Issues {
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		fmt.Fprintf(&builder, "【%s】%s", issue.Code, issue.Message)
	}
	return strings.TrimSpace(builder.String())
}

// isReviewTarget 判断问题归属是否合法。
func isReviewTarget(target string) bool {
	switch target {
	case reviewTargetPlan, reviewTargetResearch, reviewTargetContent, reviewTargetNarration, reviewTargetBoth:
		return true
	default:
		return false
	}
}

// isReviewSeverity 判断严重程度是否合法。
func isReviewSeverity(severity string) bool {
	switch severity {
	case reviewSeverityMinor, reviewSeverityMajor, reviewSeverityBlocker:
		return true
	default:
		return false
	}
}
