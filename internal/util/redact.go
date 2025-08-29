package util

import "regexp"

var (
	reEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	reToken = regexp.MustCompile(`(?i)(?:api|secret|token|key)[=:]\s*([A-Za-z0-9-_]{8,})`)
)

func RedactPII(s string) string {
	s = reEmail.ReplaceAllString(s, "[redacted-email]")
	s = reToken.ReplaceAllString(s, "$1=[redacted]")
	return s
}
