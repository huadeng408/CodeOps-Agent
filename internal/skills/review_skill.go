package skills

func reviewSkill() Skill {
	return Skill{
		Name:        "review",
		Description: "inspect changes for correctness and risk",
		Prompt:      "Review the current change set and report concrete issues first.",
		Tools:       []string{"Read", "Git", "Grep"},
	}
}
