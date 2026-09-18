package snapshot

import (
	"regexp"
	"strings"
)

type ignoreRule struct {
	base    string
	pattern *regexp.Regexp
	negate  bool
	dirOnly bool
}

func parseIgnore(base string, data []byte) ([]ignoreRule, error) {
	var rules []ignoreRule
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		line = trimTrailingSpaces(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rule := ignoreRule{base: base}
		if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
			line = line[1:]
		} else if strings.HasPrefix(line, "!") {
			rule.negate = true
			line = line[1:]
		}
		rule.dirOnly = strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		anchored := strings.HasPrefix(line, "/") || strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}

		glob := globRegexp(line)
		var expression string
		if anchored {
			expression = "^" + glob + "$"
		} else {
			expression = `(^|/)` + glob + "$"
		}
		compiled, err := regexp.Compile(expression)
		if err != nil {
			return nil, err
		}
		rule.pattern = compiled
		rules = append(rules, rule)
	}
	return rules, nil
}

func ignored(path string, isDir bool, rules []ignoreRule) bool {
	ignored := false
	for _, rule := range rules {
		if rule.dirOnly && !isDir {
			continue
		}
		rel := path
		if rule.base != "" {
			prefix := rule.base + "/"
			if !strings.HasPrefix(path, prefix) {
				continue
			}
			rel = strings.TrimPrefix(path, prefix)
		}
		if rule.pattern.MatchString(rel) {
			ignored = !rule.negate
		}
	}
	return ignored
}

func globRegexp(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i += 2
				if i < len(pattern) && pattern[i] == '/' {
					i++
					b.WriteString(`(?:.*/)?`)
				} else {
					b.WriteString(`.*`)
				}
			} else {
				i++
				b.WriteString(`[^/]*`)
			}
		case '?':
			i++
			b.WriteString(`[^/]`)
		case '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				i++
				continue
			}
			end += i + 1
			class := pattern[i+1 : end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteByte('[')
			b.WriteString(class)
			b.WriteByte(']')
			i = end + 1
		case '\\':
			if i+1 < len(pattern) {
				b.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
				i += 2
			} else {
				b.WriteString(`\\`)
				i++
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}
	return b.String()
}

func trimTrailingSpaces(line string) string {
	for len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\t') {
		backslashes := 0
		for i := len(line) - 2; i >= 0 && line[i] == '\\'; i-- {
			backslashes++
		}
		if backslashes%2 == 1 {
			return line[:len(line)-2] + line[len(line)-1:]
		}
		line = line[:len(line)-1]
	}
	return line
}
