package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/app"
)

const alexaPrincipalNamespace = "alexa"

// AsAppRepository adapts the SQLite store to the application repository port.
func AsAppRepository(store *Store) app.Repository {
	if store == nil {
		return nil
	}
	return &appRepository{store: store}
}

type appRepository struct {
	store *Store
}

func (r *appRepository) SaveInteraction(ctx context.Context, interaction app.Interaction) (int64, error) {
	return r.store.SaveInteraction(ctx, Interaction{
		ID:           interaction.ID,
		CreatedAt:    interaction.CreatedAt,
		AlexaUserID:  principalKey(interaction.Principal),
		SessionID:    interaction.ConversationID,
		Provider:     interaction.Provider,
		Model:        interaction.Model,
		Prompt:       interaction.Prompt,
		ShortAnswer:  interaction.ShortAnswer,
		FullAnswer:   interaction.FullAnswer,
		LatencyMS:    interaction.Latency.Milliseconds(),
		Status:       interaction.Status,
		ErrorMessage: interaction.ErrorMessage,
	})
}

func (r *appRepository) GetLatestResponse(ctx context.Context, principal app.Principal) (*app.Interaction, error) {
	interaction, err := r.store.GetLatestResponse(ctx, principalKey(principal))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, app.ErrRepositoryNotFound
		}
		return nil, err
	}
	return &app.Interaction{
		ID:             interaction.ID,
		CreatedAt:      interaction.CreatedAt,
		Principal:      principal,
		ConversationID: interaction.SessionID,
		Provider:       interaction.Provider,
		Model:          interaction.Model,
		Prompt:         interaction.Prompt,
		ShortAnswer:    interaction.ShortAnswer,
		FullAnswer:     interaction.FullAnswer,
		Latency:        time.Duration(interaction.LatencyMS) * time.Millisecond,
		Status:         interaction.Status,
		ErrorMessage:   interaction.ErrorMessage,
	}, nil
}

// principalKey preserves existing Alexa history keys while namespacing any
// future adapter. A later migration can make the Alexa prefix explicit without
// losing access to rows written by the original schema.
func principalKey(principal app.Principal) string {
	namespace := strings.TrimSpace(principal.Namespace)
	subject := strings.TrimSpace(principal.Subject)
	if namespace == "" || namespace == alexaPrincipalNamespace {
		return subject
	}
	return namespace + ":" + subject
}
