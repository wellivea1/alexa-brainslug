package skill

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const (
	interactionModelPath = "interaction-model.json"
	askIntentName        = "AskGPTIntent"
	querySlotName        = "Query"
	querySlotType        = "AMAZON.SearchQuery"
	queryPromptID        = "Elicit.Slot.Query"
)

type interactionModelDocument struct {
	InteractionModel interactionModel `json:"interactionModel"`
}

type interactionModel struct {
	LanguageModel languageModel `json:"languageModel"`
	Dialog        dialogModel   `json:"dialog"`
	Prompts       []modelPrompt `json:"prompts"`
}

type languageModel struct {
	InvocationName string        `json:"invocationName"`
	Intents        []modelIntent `json:"intents"`
}

type dialogModel struct {
	DelegationStrategy string        `json:"delegationStrategy"`
	Intents            []modelIntent `json:"intents"`
}

type modelIntent struct {
	Name               string      `json:"name"`
	DelegationStrategy string      `json:"delegationStrategy"`
	Slots              []modelSlot `json:"slots"`
	Samples            []string    `json:"samples"`
}

type modelSlot struct {
	Name                string      `json:"name"`
	Type                string      `json:"type"`
	ElicitationRequired bool        `json:"elicitationRequired"`
	Prompts             slotPrompts `json:"prompts"`
}

type slotPrompts struct {
	Elicitation string `json:"elicitation"`
}

type modelPrompt struct {
	ID         string            `json:"id"`
	Variations []promptVariation `json:"variations"`
}

type promptVariation struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func TestInteractionModelQuestionContract(t *testing.T) {
	rawModel, err := os.ReadFile(interactionModelPath)
	if err != nil {
		t.Fatalf("read %s: %v", interactionModelPath, err)
	}

	var document interactionModelDocument
	if err := json.Unmarshal(rawModel, &document); err != nil {
		t.Fatalf("decode %s: %v", interactionModelPath, err)
	}

	model := document.InteractionModel
	if model.LanguageModel.InvocationName != "brain slug" {
		t.Errorf("invocation name = %q", model.LanguageModel.InvocationName)
	}

	askIntent := requireIntent(t, model.LanguageModel.Intents, askIntentName)
	querySlot := requireSlot(t, askIntent.Slots, querySlotName)
	if querySlot.Type != querySlotType {
		t.Errorf("Query slot type = %q, want %q", querySlot.Type, querySlotType)
	}
	if len(askIntent.Samples) == 0 {
		t.Fatal("AskGPTIntent has no carrier samples")
	}
	for _, sample := range askIntent.Samples {
		if strings.TrimSpace(sample) == "{Query}" || !strings.Contains(sample, "{Query}") {
			t.Errorf("sample %q must contain a carrier phrase and {Query}", sample)
		}
	}

	if model.Dialog.DelegationStrategy != "SKILL_RESPONSE" {
		t.Errorf("skill delegation strategy = %q", model.Dialog.DelegationStrategy)
	}
	dialogIntent := requireIntent(t, model.Dialog.Intents, askIntentName)
	if dialogIntent.DelegationStrategy != "ALWAYS" {
		t.Errorf("AskGPTIntent delegation strategy = %q", dialogIntent.DelegationStrategy)
	}
	dialogQuery := requireSlot(t, dialogIntent.Slots, querySlotName)
	if !dialogQuery.ElicitationRequired {
		t.Error("Query slot must require elicitation")
	}
	if dialogQuery.Prompts.Elicitation != queryPromptID {
		t.Errorf("Query elicitation prompt = %q, want %q", dialogQuery.Prompts.Elicitation, queryPromptID)
	}

	prompt := requirePrompt(t, model.Prompts, queryPromptID)
	if len(prompt.Variations) == 0 || strings.TrimSpace(prompt.Variations[0].Value) == "" {
		t.Fatal("Query elicitation prompt has no speakable variation")
	}
}

func requireIntent(t *testing.T, intents []modelIntent, name string) modelIntent {
	t.Helper()
	for _, intent := range intents {
		if intent.Name == name {
			return intent
		}
	}
	t.Fatalf("intent %q is missing", name)
	return modelIntent{}
}

func requireSlot(t *testing.T, slots []modelSlot, name string) modelSlot {
	t.Helper()
	for _, slot := range slots {
		if slot.Name == name {
			return slot
		}
	}
	t.Fatalf("slot %q is missing", name)
	return modelSlot{}
}

func requirePrompt(t *testing.T, prompts []modelPrompt, id string) modelPrompt {
	t.Helper()
	for _, prompt := range prompts {
		if prompt.ID == id {
			return prompt
		}
	}
	t.Fatalf("prompt %q is missing", id)
	return modelPrompt{}
}
