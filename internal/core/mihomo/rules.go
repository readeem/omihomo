package mihomo

import (
	"fmt"
	"regexp"
	"strings"
)

// Mihomo's backtracking matcher still needs a timeout even for Go syntax:
// nested quantifiers such as (a+)+ are legal in both engines.
func checkRuleRegex(typ, value string) error {
	if strings.HasSuffix(typ, "-REGEX") {
		if _, err := regexp.Compile(value); err != nil {
			return fmt.Errorf("use Go-compatible regex syntax: %w", err)
		}
		return nil
	}
	if typ != "AND" && typ != "OR" && typ != "NOT" && typ != "SUB-RULE" {
		return nil
	}
	if len(value) < 2 || value[0] != '(' || value[len(value)-1] != ')' {
		return nil
	}
	inner := value[1 : len(value)-1]
	if typ == "SUB-RULE" {
		typ, value, _ := strings.Cut(inner, ",")
		return checkRuleRegex(strings.TrimSpace(typ), strings.TrimSpace(value))
	}
	depth, start := 0, 0
	for i, c := range inner {
		if c == '(' {
			if depth == 0 {
				start = i + 1
			}
			depth++
		}
		if c == ')' {
			depth--
			if depth == 0 {
				typ, value, _ := strings.Cut(inner[start:i], ",")
				if err := checkRuleRegex(strings.TrimSpace(typ), strings.TrimSpace(value)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
