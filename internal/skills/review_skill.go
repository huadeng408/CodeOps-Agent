package skills

func reviewSkill() Skill {
	return Skill{
		Name:        "review",
		Description: "review changes for correctness and risk",
		Prompt:      "Review the current change set and report concrete correctness, security, and regression risks first.",
		Tools:       []string{"Read", "Git", "Grep"},
	}
}
