package safety

import (
	"strings"
	"testing"
)

func TestGitEnvironmentDisablesImplicitLazyFetch(t *testing.T) {
	values := map[string]string{}
	for _, entry := range ScrubGitEnvironment([]string{"GIT_NO_LAZY_FETCH=0", "GIT_ALTERNATE_OBJECT_DIRECTORIES=unapproved", "PATH=toolchain"}) {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	if values["GIT_NO_LAZY_FETCH"] != "1" || values["GIT_ALTERNATE_OBJECT_DIRECTORIES"] != "" {
		t.Fatal("ambient implicit fetch/object controls were not removed")
	}
}
