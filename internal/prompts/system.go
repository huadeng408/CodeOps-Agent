package prompts

import (
	"sort"
	"strings"
)

type PromptSection struct {
	Name      string
	Content   string
	Cacheable bool
	Priority  int
}

type Builder struct {
	sections []PromptSection
}

func NewBuilder() *Builder {
	return &Builder{}
}

func (b *Builder) Add(section PromptSection) *Builder {
	b.sections = append(b.sections, section)
	return b
}

func (b *Builder) Build() string {
	sections := append([]PromptSection(nil), b.sections...)
	sort.SliceStable(sections, func(i, j int) bool {
		if sections[i].Cacheable != sections[j].Cacheable {
			return sections[i].Cacheable
		}
		return sections[i].Priority < sections[j].Priority
	})

	chunks := make([]string, 0, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section.Content) == "" {
			continue
		}
		chunks = append(chunks, section.Content)
	}
	return strings.Join(chunks, "\n\n")
}
