// Package speech shapes model output into text that is safe to hand to a
// text-to-speech voice.
//
// Models answer in Markdown by default. Alexa speaks the response literally,
// so asterisks, backticks, and heading markers are read aloud or mangled.
// Everything here is provider-agnostic: it operates on the finished answer,
// not on any provider's wire format.
package speech

import (
	"regexp"
	"strings"
)

// TruncationNotice is appended when an answer is shortened for speech. It has
// to make sense spoken aloud and must not promise a retrieval route that does
// not exist yet.
const TruncationNotice = "That is the short version."

var (
	fencedCode   = regexp.MustCompile("(?s)```.*?```")
	inlineCode   = regexp.MustCompile("`([^`]*)`")
	markdownLink = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	headingMark  = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+`)
	listMark     = regexp.MustCompile(`(?m)^\s{0,3}([-*+]|\d+\.)\s+`)
	blockQuote   = regexp.MustCompile(`(?m)^\s{0,3}>\s?`)
	emphasis     = regexp.MustCompile(`(\*\*|__|\*|_)`)
	tableRow     = regexp.MustCompile(`(?m)^\s*\|.*\|\s*$`)
	repeatedWS   = regexp.MustCompile(`[ \t]{2,}`)
	blankLines   = regexp.MustCompile(`\n{3,}`)
)

// Strip removes Markdown syntax that does not survive being read aloud. It
// preserves the text inside links and inline code rather than dropping it.
func Strip(text string) string {
	out := fencedCode.ReplaceAllString(text, " ")
	out = markdownLink.ReplaceAllString(out, "$1")
	out = inlineCode.ReplaceAllString(out, "$1")
	out = tableRow.ReplaceAllString(out, "")
	out = headingMark.ReplaceAllString(out, "")
	out = listMark.ReplaceAllString(out, "")
	out = blockQuote.ReplaceAllString(out, "")
	out = emphasis.ReplaceAllString(out, "")
	out = repeatedWS.ReplaceAllString(out, " ")
	out = blankLines.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

// Summarize returns a spoken-length version of text, capped at maxWords.
//
// When truncation is needed it prefers to end on the last sentence boundary in
// the second half of the kept text, so the answer does not stop mid-clause,
// and appends TruncationNotice. Text already within the limit is returned with
// Markdown stripped and nothing appended.
func Summarize(text string, maxWords int) string {
	cleaned := Strip(text)
	if cleaned == "" {
		return ""
	}

	words := strings.Fields(cleaned)
	if maxWords <= 0 || len(words) <= maxWords {
		return cleaned
	}

	summary := strings.Join(words[:maxWords], " ")
	if idx := strings.LastIndexAny(summary, ".!?"); idx > len(summary)/2 {
		summary = strings.TrimSpace(summary[:idx+1])
	}
	return summary + " " + TruncationNotice
}
