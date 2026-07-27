package skills

import "testing"

func TestNewManagerRegistersCommitSkill(t *testing.T) {
	manager := NewManager()
	skill, ok := manager.Get("commit")
	if !ok {
		t.Fatal("expected the commit skill to be registered in NewManager")
	}
	if skill.Name != "commit" {
		t.Fatalf("unexpected skill name: %q", skill.Name)
	}
	if skill.Prompt == "" {
		t.Fatal("commit skill must carry a prompt template")
	}
	if skill.Description == "" {
		t.Fatal("commit skill must carry a description")
	}
}

func TestNewManagerRegistersAllBuiltins(t *testing.T) {
	manager := NewManager()
	for _, name := range []string{"init", "review", "security", "commit"} {
		if _, ok := manager.Get(name); !ok {
			t.Errorf("expected built-in skill %q to be registered", name)
		}
	}
}
