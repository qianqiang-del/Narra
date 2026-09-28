package classroom

// maxPlanPages 是计划页数上限，与 outline.md 的约定保持一致。
const maxPlanPages = 15

// maxPlanPageIDRunes 是页标识符的长度上限，超出按字符截断。
const maxPlanPageIDRunes = 64

// minPlanBriefRunes 是一页内容摘要的最短长度。
const minPlanBriefRunes = 10

// minLearningObjectiveRunes 是一页学习目标的最短长度。
const minLearningObjectiveRunes = 5

// ClassroomPlan 是一次课堂生成的完整计划，同时作为前端大纲与段二执行的唯一事实源。
type ClassroomPlan struct {
	Version            int          `json:"version"`
	Title              string       `json:"title"`
	LearningObjectives []string     `json:"learning_objectives"`
	Audience           PlanAudience `json:"audience"`
	Pages              []PlanPage   `json:"pages"`
}

// PlanAudience 是计划面向的学员画像。
type PlanAudience struct {
	Level      string `json:"level"`
	Background string `json:"background"`
}

// PlanPage 是计划里的一页。SceneID 是场景行建立后回填的，不进 JSON。
type PlanPage struct {
	PlanID            string   `json:"plan_id"`
	Order             int      `json:"order"`
	Type              string   `json:"type"`
	Title             string   `json:"title"`
	Brief             string   `json:"brief"`
	LearningObjective string   `json:"learning_objective"`
	Prerequisites     []string `json:"prerequisites"`
	SuggestedTools    []string `json:"suggested_tools"`
	NarrationFocus    []string `json:"narration_focus"`
	EstimatedSeconds  int      `json:"estimated_seconds"`
	SceneID           uint64   `json:"-"`
}
