package host

// Copied from aphrollo-tools internal/workspace/nettransient.go at 12b3724e
// (netFailureMarks, maxRetryAfter, retryAfter, retryAfterOf,
// isTransientNetError). Incident: gh-500-retry, a wait of an hour met one 500
// from GitHub's paginated check-runs read and gave up on a pull request that
// was fine. What changed: the marks are matched at a word end ("http 5000" is
// no 500), and the retry itself is the caller's, driven by its clock.

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// transientMarks are the spellings, lower-cased, of a gh read that failed to
// reach GitHub, was cut off on the way, or was told to slow down, in gh's, Go's
// and Windows's own words. The errors come back flattened to text, so the text
// is what is read. Source of each: the aphrollo table above; "http 429" and the
// 5xx are GitHub's status lines as gh prints them ("HTTP 502: Bad Gateway");
// "unexpected end of json input" is gh failing to parse the error page of a
// 500 on a paginated read (GitHub, 2026-10-07); "timed out after" is the text
// of a gh child that ran out of its deadline (see GH.ghWithin).
var transientMarks = []string{
	"dial tcp",
	"connectex",
	"i/o timeout",
	"connection reset",
	"connection refused",
	"connection aborted",
	"forcibly closed",
	"tls handshake timeout",
	"no such host",
	"temporary failure in name resolution",
	"network is unreachable",
	"unexpected eof",
	"timed out after",
	"context deadline exceeded",
	"http 429",
	"http 500",
	"http 502",
	"http 503",
	"http 504",
	"unexpected end of json input",
	"secondary rate limit",
	"api rate limit exceeded",
}

// maxRetryAfter bounds how long a rate limit's own Retry-After may park a wait.
const maxRetryAfter = 2 * time.Minute

var retryAfter = regexp.MustCompile(`(?i)retry[- ]after:?\s*(\d+)`)

// Transient reports whether err is a failure to reach GitHub, or a rate limit,
// which a later read may not meet; and not an answer GitHub gave and means (a
// 404, a missing pull request).
func Transient(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, mark := range transientMarks {
		if hasMark(msg, mark) {
			return true
		}
	}
	return false
}

// hasMark reports whether msg holds mark not followed by a digit: "http 500"
// is a 500, "http 5000" is not.
func hasMark(msg, mark string) bool {
	for rest, off := msg, 0; ; {
		i := strings.Index(rest, mark)
		if i < 0 {
			return false
		}
		end := off + i + len(mark)
		if end >= len(msg) || !unicode.IsDigit(rune(msg[end])) {
			return true
		}
		rest, off = msg[end:], end
	}
}

// RetryAfter is the wait a rate limit's answer asks for, at most two minutes;
// 0 when err names none.
func RetryAfter(err error) time.Duration {
	m := retryAfter.FindStringSubmatch(err.Error())
	if m == nil {
		return 0
	}
	n, convErr := strconv.Atoi(m[1])
	if convErr != nil || n < 0 {
		return 0
	}
	if n >= int(maxRetryAfter/time.Second) {
		return maxRetryAfter
	}
	return time.Duration(n) * time.Second
}
