package agentupdate

import (
	"fmt"
	"strconv"
	"strings"
)

type version struct {
	major int
	minor int
	patch int
	pre   []string
}

func parseVersion(input string) (version, error) {
	value := strings.TrimPrefix(strings.TrimSpace(input), "v")
	parts := strings.SplitN(value, "+", 2)
	coreAndPre := strings.SplitN(parts[0], "-", 2)
	core := strings.Split(coreAndPre[0], ".")
	if len(core) != 3 {
		return version{}, fmt.Errorf("version %q is not semantic version X.Y.Z", input)
	}
	numbers := make([]int, 3)
	for index, item := range core {
		if item == "" || (len(item) > 1 && item[0] == '0') {
			return version{}, fmt.Errorf("version %q is invalid", input)
		}
		parsed, err := strconv.Atoi(item)
		if err != nil || parsed < 0 {
			return version{}, fmt.Errorf("version %q is invalid", input)
		}
		numbers[index] = parsed
	}
	result := version{major: numbers[0], minor: numbers[1], patch: numbers[2]}
	if len(coreAndPre) == 2 {
		if coreAndPre[1] == "" {
			return version{}, fmt.Errorf("version %q is invalid", input)
		}
		result.pre = strings.Split(coreAndPre[1], ".")
		for _, identifier := range result.pre {
			if identifier == "" {
				return version{}, fmt.Errorf("version %q is invalid", input)
			}
			for _, character := range identifier {
				if !(character == '-' || character >= '0' && character <= '9' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z') {
					return version{}, fmt.Errorf("version %q is invalid", input)
				}
			}
		}
	}
	return result, nil
}

func compareVersions(first, second string) (int, error) {
	a, err := parseVersion(first)
	if err != nil {
		return 0, err
	}
	b, err := parseVersion(second)
	if err != nil {
		return 0, err
	}
	for _, pair := range [][2]int{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if pair[0] < pair[1] {
			return -1, nil
		}
		if pair[0] > pair[1] {
			return 1, nil
		}
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0, nil
	}
	if len(a.pre) == 0 {
		return 1, nil
	}
	if len(b.pre) == 0 {
		return -1, nil
	}
	limit := min(len(a.pre), len(b.pre))
	for index := range limit {
		left, right := a.pre[index], b.pre[index]
		if left == right {
			continue
		}
		leftNumber, leftErr := strconv.Atoi(left)
		rightNumber, rightErr := strconv.Atoi(right)
		if leftErr == nil && rightErr == nil {
			if leftNumber < rightNumber {
				return -1, nil
			}
			return 1, nil
		}
		if leftErr == nil {
			return -1, nil
		}
		if rightErr == nil {
			return 1, nil
		}
		if left < right {
			return -1, nil
		}
		return 1, nil
	}
	if len(a.pre) < len(b.pre) {
		return -1, nil
	}
	if len(a.pre) > len(b.pre) {
		return 1, nil
	}
	return 0, nil
}
