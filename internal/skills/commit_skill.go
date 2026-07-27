package skills

// commitSkill 提供自动生成 conventional commit 提交信息的能力：给定当前工作区的
// git diff --stat 与 diff，由模型起草一条提交信息供用户复制使用（绝不自动提交）。
func commitSkill() Skill {
	return Skill{
		Name:        "commit",
		Description: "draft a conventional commit message from the current diff",
		Prompt:      "Given the following git diff --stat and diff, draft a single conventional-commit message (type(scope): subject + optional body). Output ONLY the message.",
		Tools:       []string{"Git"},
	}
}
