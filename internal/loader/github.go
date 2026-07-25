package loader

import "strings"

// NormalizeRepo turns a GitHub URL or path into a bare "owner/repo" string.
// It strips an optional scheme, SCP-style SSH prefix and "github.com/" prefix,
// keeps the first two path segments and drops a trailing ".git" on the repo
// segment. It returns "" when the input cannot yield owner and repo.
func NormalizeRepo(s string) string {
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "git@github.com:")
	s = strings.TrimPrefix(s, "github.com/")
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return ""
	}
	owner := parts[0]
	repo := strings.TrimSuffix(parts[1], ".git")
	if owner == "" || repo == "" {
		return ""
	}
	// A GitHub owner segment is never a hostname. If it still contains a dot,
	// an unsupported or spoofed host (e.g. "github.com.evil.com") survived the
	// prefix stripping; reject rather than emit a malformed "owner/repo".
	if strings.Contains(owner, ".") {
		return ""
	}
	return owner + "/" + repo
}

// ParseToolRef classifies a `keepkit track` argument. When arg refers to a GitHub
// repository it returns a short tool name (the repo segment) and a normalized
// "github.com/owner/repo" string. Otherwise it returns the argument unchanged as
// a plain name with isGitHub=false.
func ParseToolRef(arg string) (name, github string, isGitHub bool) {
	if !strings.Contains(arg, "github.com") {
		return arg, "", false
	}

	repo := NormalizeRepo(arg)
	if repo == "" {
		// Looks GitHub-ish but isn't parseable — never lose the input.
		return arg, "", false
	}

	parts := strings.SplitN(repo, "/", 2)
	return parts[1], "github.com/" + repo, true
}
