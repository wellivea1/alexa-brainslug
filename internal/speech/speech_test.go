package speech

import (
	"strings"
	"testing"
)

func TestStrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text untouched", "The sky is blue.", "The sky is blue."},
		{"bold and italic", "This is **very** _important_.", "This is very important."},
		{"inline code keeps content", "Run `go test ./...` first.", "Run go test ./... first."},
		{"heading marker", "## Summary\nIt works.", "Summary\nIt works."},
		{"list markers", "- first\n- second", "first\nsecond"},
		{"numbered list markers", "1. first\n2. second", "first\nsecond"},
		{"link keeps label", "See [the docs](https://example.com) for more.", "See the docs for more."},
		{"block quote", "> quoted line", "quoted line"},
		{"fenced code removed", "Before\n```go\nfmt.Println()\n```\nAfter", "Before\n \nAfter"},
		{"table rows removed", "Totals:\n| a | b |\n| 1 | 2 |", "Totals:"},
		{
			"hyphen inside a sentence is not a list marker",
			"The well-known result - which surprised us - held.",
			"The well-known result - which surprised us - held.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Strip(tt.in); got != tt.want {
				t.Errorf("Strip(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSummarizeUnderLimitIsUnchanged(t *testing.T) {
	const answer = "Sourdough is dense when the starter is underactive."
	got := Summarize(answer, 120)
	if got != answer {
		t.Errorf("expected the answer unchanged, got %q", got)
	}
	if strings.Contains(got, TruncationNotice) {
		t.Error("short answers must not be given a truncation notice")
	}
}

func TestSummarizeTruncatesOnSentenceBoundary(t *testing.T) {
	long := strings.Repeat("word ", 40) + "End of thought. " + strings.Repeat("tail ", 40)

	got := Summarize(long, 50)

	if !strings.HasSuffix(got, TruncationNotice) {
		t.Errorf("expected the truncation notice, got %q", got)
	}
	if !strings.Contains(got, "End of thought.") {
		t.Errorf("expected the sentence boundary to be kept, got %q", got)
	}
	if strings.Contains(got, "tail") {
		t.Errorf("expected text past the boundary to be dropped, got %q", got)
	}
}

func TestSummarizeEmptyInput(t *testing.T) {
	if got := Summarize("   \n\t ", 120); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestSummarizeStripsMarkdownBeforeCounting(t *testing.T) {
	got := Summarize("**Bold** answer here.", 120)
	if strings.Contains(got, "*") {
		t.Errorf("markdown survived into spoken text: %q", got)
	}
}
