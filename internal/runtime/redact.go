package runtime

import "regexp"

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer\s+)?)([^\s,;]+)`),
	regexp.MustCompile(`(?i)(psk|password|token|authorization|cookie|secret|key)\s*[:=]\s*([^\s,;]+)`),
	regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._~+/-]+=*`),
}
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func Redact(value string) string {
	value = ansiPattern.ReplaceAllString(value, "")
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllString(value, "$1=[REDACTED]")
	}
	return value
}
