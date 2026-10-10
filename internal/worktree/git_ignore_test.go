package worktree

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGitIgnoreAvoidsExponentialWildcardBacktracking(t *testing.T) {
	matcher, err := compileGitIgnore(strings.Repeat("*a", 25)+"b", false, false)
	if err != nil {
		t.Fatal(err)
	}
	rules := &gitIgnoreRules{rules: []gitIgnoreRule{{matcher: matcher}}}
	done := make(chan bool, 1)
	go func() {
		ignored, _ := gitIgnored(context.Background(), rules, strings.Repeat("a", 200), false)
		done <- ignored
	}()
	select {
	case ignored := <-done:
		if ignored {
			t.Fatal("nonmatching wildcard admitted")
		}
	case <-time.After(time.Second):
		t.Fatal("ignore matcher is stuck in backtracking")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gitIgnored(ctx, rules, "source.py", false); !errors.Is(err, context.Canceled) {
		t.Fatal("ignore matching ignored cancellation")
	}
}

func TestCaptureBaselineMatchesNativeUTF8IgnoreRules(t *testing.T) {
	for _, test := range []struct {
		rule, name string
		fold       bool
	}{
		{"?.py", "你.py", false}, {"???.py", "你.py", false},
		{"?.py", "é.py", false}, {"??.py", "é.py", false},
		{"[é].py", "é.py", false}, {"你.py", "你.py", false},
		{"É.py", "é.py", true}, {"[[:alpha:]].py", "a.py", false},
		{"foo[!a]bar.py", "foo/bar.py", false},
		{"foo[!-z]bar.py", "foo/bar.py", false},
		{"foo[!-z]bar.py", "fooZbar.py", false},
	} {
		t.Run(test.rule+test.name, func(t *testing.T) {
			root := taskRepository(t)
			if test.fold {
				baselineGit(t, root, "config", "core.ignorecase", "true")
			}
			writeBaselineFile(t, root, ".gitignore", test.rule+"\n")
			writeBaselineFile(t, root, test.name, "fixture source")
			_, nativeError := gitOutput(context.Background(), root, "check-ignore", "--quiet", test.name)
			baseline, err := NewManager(root, "HEAD").CaptureBaseline(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, file := range baseline.Files {
				if file.Path == test.name {
					found = true
				}
			}
			if found != (nativeError != nil) {
				t.Fatalf("UTF-8 ignore inventory differs from native Git: admitted=%t, native_error=%v", found, nativeError)
			}
		})
	}
}

func TestCaptureBaselineUsesEnabledWorktreeIgnoreConfig(t *testing.T) {
	for _, enabled := range []string{"true", "false", "0", ""} {
		t.Run(enabled, func(t *testing.T) {
			root := taskRepository(t)
			baselineGit(t, root, "config", "core.ignorecase", "false")
			baselineGit(t, root, "config", "extensions.worktreeConfig", enabled)
			writeBaselineFile(t, root, ".git/config.worktree", "[core]\nignorecase = true\n")
			writeBaselineFile(t, root, ".gitignore", "Hidden.TMP\n")
			writeBaselineFile(t, root, "hidden.tmp", "fixture source")
			_, nativeError := gitOutput(context.Background(), root, "check-ignore", "--quiet", "hidden.tmp")
			baseline, err := NewManager(root, "HEAD").CaptureBaseline(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, file := range baseline.Files {
				if file.Path == "hidden.tmp" {
					found = true
				}
			}
			if found != (nativeError != nil) {
				t.Fatal("worktree config differs from native Git")
			}
		})
	}
}
