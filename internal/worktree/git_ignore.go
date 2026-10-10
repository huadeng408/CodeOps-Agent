package worktree

import (
	"context"
	"errors"
	"os"
	"regexp"
	"regexp/syntax"
	"strings"
)

type gitIgnoreRule struct {
	domain            string
	matcher           *regexp.Regexp
	negate, directory bool
}

type gitIgnoreRules struct {
	parent *gitIgnoreRules
	rules  []gitIgnoreRule
}

type gitIgnoreBudget struct{ bytes, rules int }

func readIgnorePatterns(root *os.Root, name, domain string, inherited *gitIgnoreRules, fold bool, budget *gitIgnoreBudget) (*gitIgnoreRules, error) {
	data, exists, err := readBaselineFile(root, name, maxBaselineFileBytes)
	if err != nil || !exists {
		return inherited, err
	}
	rules := &gitIgnoreRules{parent: inherited}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		for strings.HasSuffix(line, " ") {
			slashes := 0
			for i := len(line) - 2; i >= 0 && line[i] == '\\'; i-- {
				slashes++
			}
			if slashes%2 == 1 {
				break
			}
			line = strings.TrimSuffix(line, " ")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 4096 {
			return nil, errors.New("Git ignore pattern length limit exceeded")
		}
		budget.bytes += len(line)
		budget.rules++
		if budget.bytes > 8<<20 || budget.rules > 20_000 {
			return nil, errors.New("Git ignore aggregate limit exceeded")
		}
		rule := gitIgnoreRule{domain: gitMatchText(domain), negate: strings.HasPrefix(line, "!"), directory: strings.HasSuffix(line, "/")}
		if rule.negate {
			line = line[1:]
		}
		anchored := strings.HasPrefix(line, "/")
		line = strings.TrimPrefix(strings.TrimSuffix(line, "/"), "/")
		if line == "" {
			continue
		}
		rule.matcher, err = compileGitIgnore(line, anchored, fold)
		if err != nil {
			return nil, err
		}
		rules.rules = append(rules.rules, rule)
	}
	if len(rules.rules) == 0 {
		return inherited, nil
	}
	return rules, nil
}

func gitIgnored(ctx context.Context, rules *gitIgnoreRules, name string, directory bool) (bool, error) {
	name = gitMatchText(name)
	for scope := rules; scope != nil; scope = scope.parent {
		for i := len(scope.rules) - 1; i >= 0; i-- {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			rule := scope.rules[i]
			relative := strings.TrimPrefix(name, rule.domain)
			if (!rule.directory || directory) && strings.HasPrefix(name, rule.domain) && rule.matcher.MatchString(relative) {
				return !rule.negate, nil
			}
		}
	}
	return false, nil
}

// Map each non-ASCII byte to a caseless rune: Git wildcards match UTF-8 bytes.
func gitMatchText(value string) string {
	for i := 0; i < len(value); i++ {
		if value[i] < 128 {
			continue
		}
		var converted strings.Builder
		converted.WriteString(value[:i])
		for ; i < len(value); i++ {
			if value[i] < 128 {
				converted.WriteByte(value[i])
			} else {
				converted.WriteRune(0xe000 + rune(value[i]))
			}
		}
		return converted.String()
	}
	return value
}

// RE2 avoids recursive wildcard backtracking on repository-controlled patterns.
func compileGitIgnore(pattern string, anchored, fold bool) (*regexp.Regexp, error) {
	var expression strings.Builder
	if fold {
		expression.WriteString("(?i)")
	}
	if anchored || strings.Contains(pattern, "/") {
		expression.WriteByte('^')
	} else {
		expression.WriteString("(?:^|/)")
	}
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
			if i == len(pattern) {
				return nil, errors.New("unsupported Git ignore escape")
			}
			expression.WriteString(regexp.QuoteMeta(gitMatchText(pattern[i : i+1])))
		case '*':
			end := i + 1
			for end < len(pattern) && pattern[end] == '*' {
				end++
			}
			if end-i == 2 && (i == 0 || pattern[i-1] == '/') && (end == len(pattern) || pattern[end] == '/') {
				if end < len(pattern) {
					expression.WriteString("(?:[^/]+/)*")
					end++
				} else {
					expression.WriteString(".*")
				}
			} else {
				expression.WriteString("[^/]*")
			}
			i = end - 1
		case '?':
			expression.WriteString("[^/]")
		case '[':
			end := i + 1
			if end < len(pattern) && (pattern[end] == '!' || pattern[end] == '^') {
				end++
			}
			if end < len(pattern) && pattern[end] == ']' {
				end++
			}
			for end < len(pattern) && pattern[end] != ']' {
				if pattern[end] == '[' && end+1 < len(pattern) && pattern[end+1] == ':' {
					close := strings.Index(pattern[end+2:], ":]")
					if close < 0 {
						return nil, errors.New("unsupported Git ignore character class")
					}
					end += close + 4
				} else if pattern[end] == '\\' {
					end += 2
				} else {
					end++
				}
			}
			if end >= len(pattern) {
				return nil, errors.New("unsupported Git ignore character class")
			}
			class := pattern[i+1 : end]
			if strings.Contains(class, "/") {
				return nil, errors.New("unsupported Git ignore character class")
			}
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			if strings.HasPrefix(class, "]") {
				class = `\]` + class[1:]
			}
			if strings.HasPrefix(class, "^]") {
				class = `^\]` + class[2:]
			}
			var escaped strings.Builder
			for k := 0; k < len(class); k++ {
				if class[k] == '\\' && k+1 < len(class) {
					k++
					if class[k] == '-' {
						escaped.WriteString(`\-`)
					} else {
						escaped.WriteString(regexp.QuoteMeta(gitMatchText(class[k : k+1])))
					}
				} else {
					escaped.WriteString(gitMatchText(class[k : k+1]))
				}
			}
			parsed, err := syntax.Parse("["+escaped.String()+"]", syntax.Perl)
			if err != nil {
				return nil, errors.New("unsupported Git ignore character class")
			}
			if parsed.Op == syntax.OpCharClass {
				var ranges []rune
				for k := 0; k < len(parsed.Rune); k += 2 {
					low, high := parsed.Rune[k], parsed.Rune[k+1]
					if low <= '/' && high >= '/' {
						if low < '/' {
							ranges = append(ranges, low, '/'-1)
						}
						if high > '/' {
							ranges = append(ranges, '/'+1, high)
						}
					} else {
						ranges = append(ranges, low, high)
					}
				}
				parsed.Rune = ranges
				if len(ranges) == 0 {
					parsed.Op = syntax.OpNoMatch
				}
			}
			expression.WriteString(parsed.String())
			i = end
		default:
			expression.WriteString(regexp.QuoteMeta(gitMatchText(pattern[i : i+1])))
		}
	}
	expression.WriteByte('$')
	matcher, err := regexp.Compile(expression.String())
	if err != nil {
		return nil, errors.New("unsupported Git ignore pattern")
	}
	return matcher, nil
}
