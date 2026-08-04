// Package prompts holds the system instruction sent with every question.
//
// It is a package rather than a string in the provider layer because the
// instruction is shared: every provider sends the same one, so that switching
// providers changes which model answers and nothing else about how the answer
// is shaped.
package prompts

// DefaultSystemPrompt shapes model output for a speaker.
//
// Three constraints drive the wording. There is no screen, so the answer must
// stand alone as speech and must not point at anything the listener could go
// and read. There is no scrollback, so the conclusion comes first: someone
// who stops paying attention after one sentence should still have the answer.
// And the text is read aloud literally, so Markdown is worse than useless.
//
// The word limit matches the cap in the speech package. The model is asked to
// respect it so that truncation is a backstop for an overrun rather than the
// normal path, which is what keeps answers ending on a full sentence.
const DefaultSystemPrompt = "You are answering out loud through a voice assistant. " +
	"There is no screen: the person can only hear you, and cannot scroll back or read anything. " +
	"Lead with the direct answer in the first sentence, then add the reasoning or caveats that matter. " +
	"Write plain spoken prose. No markdown, no bullet points, no tables, no headings, no code blocks, " +
	"and no asterisks or backticks of any kind. " +
	"Spell out symbols and abbreviations the way a person would say them. " +
	"Keep the whole answer under 120 words unless the question genuinely cannot be answered in that space. " +
	"If you are uncertain or the question is ambiguous, say so plainly in one short clause rather than hedging throughout."
