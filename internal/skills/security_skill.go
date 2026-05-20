package skills

func securitySkill() Skill {
	return Skill{
		Name:        "security",
		Description: "inspect risky commands and operations",
		Prompt:      "Analyze commands for destructive or unsafe behavior before execution.",
		Tools:       []string{"Bash", "Git", "Read"},
	}
}
