package transcript

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
)

const (
	// maxRelativeRecovery bounds relative backoff durations parsed from provider errors so corrupt or huge values
	// fall back to the caller's bounded retry instead of stranding a scheduled run.
	maxRelativeRecovery = 7 * 24 * time.Hour
	// minEpochSeconds rejects small integers (e.g. "resets_at": 60) from being misread as 1970 Unix timestamps.
	minEpochSeconds = 1_000_000_000
	maxEpochSeconds = 4_102_444_800 // 2100-01-01T00:00:00Z
	// An error may carry a JSON fragment. End at the field boundary so a malformed exponent cannot leave a valid prefix.
	jsonRecoveryValue    = `("[^"\r\n]*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)\s*(?:[,}]|$)`
	recoveryDurationPart = `\d+(?:\.\d+)?\s*(?:milliseconds?|ms|seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h)`
)

var (
	jsonRecoveryTimeRe = lazyre.New(`(?i)"(?:resets?_at|resetsAt|resetAt|retry_at|retryAt|available_at|availableAt|available_again_at|availableAgainAt|recover(?:y)?_at|recover(?:y)?At)"\s*:\s*` + jsonRecoveryValue)
	jsonRetryAfterRe   = lazyre.New(`(?i)"(retry_after|retryAfter|retry_after_seconds|retryAfterSeconds|retry_after_ms|retryAfterMs)"\s*:\s*` + jsonRecoveryValue)
	keywordRFC3339Re   = lazyre.New(`(?i)\b(?:available again at|available at|resets?(?:\s+at)?|retry\s+after|retry\s+at|try\s+again\s+at|recovers?(?:\s+at)?|resets?_at|retry_at|available_at|recovery_at)\s*[:=]?\s*(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2}))`)
	retryAfterDateRe   = lazyre.New(`(?i)\bretry[-_]after\s*[:=]\s*([^\r\n;)\]}"]+)`)
	retryAfterSecsRe   = lazyre.New(`(?i)\bretry[-_]after\s*[:=]\s*([^\r\n;)\]}",]+)`)
	tryAgainPhraseRe   = lazyre.New(`(?i)\b(?:try again in|retry (?:again )?in|retry after)\s+(` + recoveryDurationPart +
		`(?:(?:\s*,\s*(?:and\s+)?|\s+and\s+|\s*)` + recoveryDurationPart + `)*)\b`)
	tryAgainPartRe = lazyre.New(`(\d+(?:\.\d+)?)\s*([A-Za-z]+)`)
)

// ProviderRecoveryAt parses an explicit, machine-readable recovery time from a terminal provider error message.
// Absolute timestamps with an explicit date and time zone are parsed without guessing. Undated UTC clocks and
// relative waits (Retry-After seconds, JSON retry_after, "try again in ...") are anchored to at and return false
// when at is zero. Malformed, non-positive, or out-of-range values return false so callers fall back safely.
func ProviderRecoveryAt(text string, at time.Time) (time.Time, bool) {
	if strings.TrimSpace(text) == "" {
		return time.Time{}, false
	}
	if t, ok := parseJSONRecoveryTime(text); ok {
		return t, true
	}
	if t, ok := parseKeywordRFC3339(text); ok {
		return t, true
	}
	if t, ok := parseRetryAfterDate(text); ok {
		return t, true
	}
	if t, ok := GatewayAvailableAgain(text, at); ok {
		return t.UTC(), true
	}
	if t, ok := usageLimitClockReset(text, at); ok {
		return t.UTC(), true
	}
	if t, ok := parseJSONRetryAfter(text, at); ok {
		return t, true
	}
	if t, ok := parseRetryAfterSeconds(text, at); ok {
		return t, true
	}
	if t, ok := parseTryAgainPhrase(text, at); ok {
		return t, true
	}
	return time.Time{}, false
}

func parseJSONRecoveryTime(text string) (time.Time, bool) {
	for _, m := range jsonRecoveryTimeRe.FindAllStringSubmatch(text, -1) {
		raw := strings.TrimSpace(m[1])
		if strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`) {
			s := strings.TrimSpace(raw[1 : len(raw)-1])
			if t, ok := parseAbsoluteDateString(s); ok {
				return t, true
			}
			if t, ok := parseEpochRecovery(s); ok {
				return t, true
			}
			continue
		}
		if t, ok := parseEpochRecovery(raw); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseJSONRetryAfter(text string, at time.Time) (time.Time, bool) {
	for _, m := range jsonRetryAfterRe.FindAllStringSubmatch(text, -1) {
		key := strings.ToLower(m[1])
		raw := strings.TrimSpace(m[2])
		quoted := strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`)
		val := raw
		if quoted {
			val = strings.TrimSpace(raw[1 : len(raw)-1])
			if (key == "retry_after" || key == "retryafter") && val != "" {
				if t, ok := parseAbsoluteDateString(val); ok {
					return t, true
				}
			}
		}
		if at.IsZero() || val == "" || strings.HasPrefix(val, "-") {
			continue
		}
		f, err := strconv.ParseFloat(val, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
			continue
		}
		unit := time.Second
		if strings.HasSuffix(key, "ms") {
			unit = time.Millisecond
		}
		ns := f * float64(unit)
		if ns <= 0 || ns > float64(maxRelativeRecovery) {
			continue
		}
		d := time.Duration(ns)
		if d <= 0 {
			continue
		}
		return at.Add(d).UTC(), true
	}
	return time.Time{}, false
}

func parseKeywordRFC3339(text string) (time.Time, bool) {
	for _, m := range keywordRFC3339Re.FindAllStringSubmatch(text, -1) {
		if t, err := time.Parse(time.RFC3339, m[1]); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func parseRetryAfterDate(text string) (time.Time, bool) {
	for _, m := range retryAfterDateRe.FindAllStringSubmatch(text, -1) {
		v := strings.TrimRight(strings.TrimSpace(m[1]), ".")
		if t, ok := parseAbsoluteDateString(v); ok {
			return t, true
		}
		if idx := strings.Index(v, "GMT"); idx > 0 {
			if t, err := http.ParseTime(strings.TrimSpace(v[:idx+3])); err == nil {
				return t.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

func parseRetryAfterSeconds(text string, at time.Time) (time.Time, bool) {
	if at.IsZero() {
		return time.Time{}, false
	}
	for _, m := range retryAfterSecsRe.FindAllStringSubmatch(text, -1) {
		v := strings.TrimRight(strings.TrimSpace(m[1]), ".")
		if v == "" || !allASCIIDigits(v) {
			continue
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil || n == 0 || n > uint64(maxRelativeRecovery/time.Second) {
			continue
		}
		return at.Add(time.Duration(n) * time.Second).UTC(), true
	}
	return time.Time{}, false
}

func parseTryAgainPhrase(text string, at time.Time) (time.Time, bool) {
	if at.IsZero() {
		return time.Time{}, false
	}
	for _, m := range tryAgainPhraseRe.FindAllStringSubmatch(text, -1) {
		total := 0.0
		ok := true
		for _, p := range tryAgainPartRe.FindAllStringSubmatch(m[1], -1) {
			n, err := strconv.ParseFloat(p[1], 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				ok = false
				break
			}
			total += n * float64(recoveryDurationUnit(p[2]))
		}
		if !ok || total <= 0 || total > float64(maxRelativeRecovery) {
			continue
		}
		d := time.Duration(total)
		if d <= 0 {
			continue
		}
		return at.Add(d).UTC(), true
	}
	return time.Time{}, false
}

func parseAbsoluteDateString(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), true
	}
	if t, err := http.ParseTime(s); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

func parseEpochRecovery(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "-") {
		return time.Time{}, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return time.Time{}, false
	}
	if f >= 1e11 {
		if f < float64(minEpochSeconds)*1000 || f > float64(maxEpochSeconds)*1000 {
			return time.Time{}, false
		}
		return time.UnixMilli(int64(f)).UTC(), true
	}
	if f < float64(minEpochSeconds) || f > float64(maxEpochSeconds) {
		return time.Time{}, false
	}
	return time.Unix(int64(f), 0).UTC(), true
}

func allASCIIDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

func recoveryDurationUnit(name string) time.Duration {
	switch name = strings.ToLower(name); {
	case name == "ms" || strings.HasPrefix(name, "milli"):
		return time.Millisecond
	case strings.HasPrefix(name, "h"):
		return time.Hour
	case strings.HasPrefix(name, "m"):
		return time.Minute
	default:
		return time.Second
	}
}
