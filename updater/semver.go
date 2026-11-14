package updater

import (
	"strconv"
	"strings"
)

func CompareSemver(a, b string) int {
	pa := parseSemver(a)
	pb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa.nums[i] > pb.nums[i] {
			return 1
		}
		if pa.nums[i] < pb.nums[i] {
			return -1
		}
	}
	if pa.pre == "" && pb.pre != "" {
		return 1
	}
	if pa.pre != "" && pb.pre == "" {
		return -1
	}
	if pa.pre > pb.pre {
		return 1
	}
	if pa.pre < pb.pre {
		return -1
	}
	return 0
}

type semverParts struct {
	nums [3]int
	pre  string
}

func parseSemver(raw string) semverParts {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if idx := strings.IndexByte(s, '+'); idx >= 0 {
		s = s[:idx]
	}
	pre := ""
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		pre = s[idx+1:]
		s = s[:idx]
	}
	parts := strings.Split(s, ".")
	var out semverParts
	out.pre = pre
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		out.nums[i] = n
	}
	return out
}
