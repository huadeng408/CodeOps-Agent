package skills

func initSkill() Skill {
	return Skill{
		Name:        "init",
		Description: "bootstrap project instructions and baseline structure",
		Prompt:      "Initialize the repository skeleton and explain the active structure.",
		Tools:       []string{"Read", "Glob", "Grep"},
	}
}
