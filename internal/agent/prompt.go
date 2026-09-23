package agent

import (
	"embed"
	"fmt"
	"strings"

	"narra/internal/model/entity"
)

// 用 go:embed 而不是运行时读文件：前者编译进二进制，不存在部署时目录没带上的问题。
//
//go:embed prompts
var promptFS embed.FS

// 文件名写错会在包初始化时 panic（走 mustPromptFile），跑一次测试就能发现。
const (
	promptDirRoles      = "prompts/roles"
	promptFileNarration = "prompts/tasks/narration.md"
	promptFileOutline   = "prompts/tasks/outline.md"
	promptFileScene     = "prompts/tasks/scene.md"
)

// PromptTask 这次要干什么活。角色层回答「你是谁」，任务层回答「这次干什么活」。
type PromptTask string

// TaskNarration 生成期的讲稿撰写，产出 scene_segments.text。
const TaskNarration PromptTask = "narration"

// TaskOutline 生成期的排课，产出 scenes 的骨架。
const TaskOutline PromptTask = "outline"

// TaskScene 生成期的单场景内容与讲稿。
const TaskScene PromptTask = "scene"

// roleHead 全体角色共用的框架。
var roleHead = mustPromptFile(promptDirRoles + "/head.md")

// roleLayer 角色层，按 role_type 取。
//
// 同一大类共用一份：池子里 10 个学生的行为规范是同一份，它们之间「怎么说话」的差别
// 由 PresetAgent.Persona 承载，不写在这里。所以往角色池加角色不用动本文件，
// 加一个新的大类才要。
//
// 键取 entity.PresetAgentRoleType*，而不是在包内另写一份常量：数据库的 CHECK 认的是
// 那组值，两处各写一份就会出现「map 里有、CHECK 里没有」这种运行时才炸的分叉。
var roleLayer = map[string]string{
	entity.PresetAgentRoleTypeTeacher:   mustPromptFile(promptDirRoles + "/teacher.md"),
	entity.PresetAgentRoleTypeAssistant: mustPromptFile(promptDirRoles + "/assistant.md"),
	entity.PresetAgentRoleTypeStudent:   mustPromptFile(promptDirRoles + "/student.md"),
}

// taskLayer 任务层，以及只有哪个角色大类接得到这个任务。
//
// 目前只有讲稿一个任务，且只有教师有：讲解只由教师发声
// （internal/model/entity/scene_segment.go:20）。播放期的圆桌发言不在本模块，
// 那边的任务层加在 prompts/tasks/ 下，在这里补一行。
//
// 不需要角色的任务（大纲、场景内容）不在这张表里——见下面的 roleFreeTasks。
var taskLayer = map[PromptTask]struct {
	body     string
	roleType string
}{
	TaskNarration: {mustPromptFile(promptFileNarration), entity.PresetAgentRoleTypeTeacher},
}

// roleFreeTasks 不需要角色层的任务：排课与出内容不是任何角色在发言，也不该夹带角色池的人设。
var roleFreeTasks = map[PromptTask]string{
	TaskOutline: mustPromptFile(promptFileOutline),
	TaskScene:   mustPromptFile(promptFileScene),
}

// BuildSystemPrompt 装配一个角色在指定任务下的完整系统提示词。
//
// 入参是角色池里的一行。传结构体而不是几个 string，是因为 RoleType / Name / Persona
// 都是字符串，散着传太容易传错位置。
//
// 第二个返回值为 false 表示拼不出来，调用方必须处理，不要拿空串当提示词用：
// 角色大类不在角色层里，或者这个角色没有这个任务。
func BuildSystemPrompt(a entity.PresetAgent, task PromptTask) (string, bool) {
	role, ok := roleLayer[a.RoleType]
	if !ok {
		return "", false
	}

	t, ok := taskLayer[task]
	if !ok || t.roleType != a.RoleType {
		return "", false
	}

	s := joinSections(roleHead, role, t.body)
	return strings.NewReplacer("{{agentName}}", a.Name, "{{persona}}", a.Persona).Replace(s), true
}

// BuildRoleIdentity 拼一个角色的身份框架（框架+大类规范+persona 注入），不含任务指令，由调用方接圆桌发言层。
func BuildRoleIdentity(a entity.PresetAgent) (string, bool) {
	role, ok := roleLayer[a.RoleType]
	if !ok {
		return "", false
	}
	s := joinSections(roleHead, role)
	return strings.NewReplacer("{{agentName}}", a.Name, "{{persona}}", a.Persona).Replace(s), true
}

// BuildTaskPrompt 装配不需要角色的任务提示词，只拼任务层、不注入 {{agentName}} / {{persona}}。
// 返回 false 表示该 task 不在 roleFreeTasks 里（如讲稿，要用 BuildSystemPrompt）。
func BuildTaskPrompt(task PromptTask) (string, bool) {
	body, ok := roleFreeTasks[task]
	if !ok {
		return "", false
	}
	return joinSections(body), true
}

// mustPromptFile 读一个内嵌的提示词文件，读不到就 panic。
//
// 文件在编译期已经嵌进二进制，读不到只可能是路径写错，属于编程错误而不是运行时状况，
// 没有可恢复的分支。prompt_test.go 会把所有路径过一遍。
func mustPromptFile(path string) string {
	b, err := promptFS.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("agent: 内嵌提示词 %s 读不到: %v", path, err))
	}
	return string(b)
}

// joinSections 拼接提示词片段，片段之间恰好隔一个空行。
//
// 不依赖各文件自己的结尾：「这份末尾多敲了一个回车」不该改变拼出来的提示词形状，
// 而少一个回车会让下一段的标题紧贴在上一段最后一行后面（markdown 上就成了同一段）。
func joinSections(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimRight(p, "\n"))
	}
	return strings.Join(out, "\n\n") + "\n"
}
