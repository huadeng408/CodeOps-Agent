package skills

func securitySkill() Skill {
	return Skill{
		Name:        "security",
		Description: "inspect security boundaries and secrets",
		Prompt:      "Inspect trust boundaries, authorization, secret handling, injection risks, and fail-closed paths.",
		Tools:       []string{"Read", "Grep", "Bash"},
	}
}
