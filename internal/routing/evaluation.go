package routing

import (
	"context"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

// Example is a labeled route with its authorized catalog, never a live task.
type Example struct {
	Name     string           `json:"name"`
	Prompt   string           `json:"prompt"`
	Catalog  control.Catalog  `json:"catalog"`
	Expected control.Decision `json:"expected"`
}

// Evaluation reports classification accuracy separately from inference latency and usage.
type Evaluation struct {
	Cases    int      `json:"cases"`
	Correct  int      `json:"correct"`
	Accuracy float64  `json:"accuracy"`
	Tokens   int64    `json:"tokens"`
	Millis   int64    `json:"millis"`
	Failed   []string `json:"failed"`
}

// Evaluate calls a router without dispatching any work. The caller must choose
// representative authorized catalogs and review failures before enabling automatic mode.
func Evaluate(ctx context.Context, router control.Router, examples []Example) Evaluation {
	report := Evaluation{Cases: len(examples)}
	for i := range examples {
		example := &examples[i]
		start := time.Now()
		decision, usage, err := router.Propose(ctx, example.Prompt, example.Catalog)
		report.Millis += time.Since(start).Milliseconds()
		report.Tokens += usage.RoutingTokens
		want := &example.Expected
		correct := err == nil && decision.Action == want.Action && decision.HostID == want.HostID && decision.ProjectID == want.ProjectID && decision.ConversationID == want.ConversationID && decision.Model == want.Model && decision.Effort == want.Effort && decision.Profile == want.Profile && decision.TargetJobID == want.TargetJobID
		if correct {
			report.Correct++
		} else {
			report.Failed = append(report.Failed, example.Name)
		}
	}
	if report.Cases != 0 {
		report.Accuracy = float64(report.Correct) / float64(report.Cases)
	}
	return report
}
