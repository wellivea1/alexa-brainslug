package config

import "strings"

// Supported wire formats are independent of the provider identity stored in history.
const (
	FormatResponses       = "responses"
	FormatChatCompletions = "chat-completions"
	FormatMessages        = "messages"
)

// ProviderDefinition describes one compiled-in provider route. Adding a route
// requires a table entry and a configuration field, not changes in the manager,
// dashboard, credential handling, or settings file schema.
type ProviderDefinition struct {
	ID                  string
	Name                string
	TabName             string
	PanelID             string
	Monogram            string
	EnvPrefix           string
	DefaultBaseURL      string
	DefaultOutputTokens int
	Format              string
	EndpointKind        string
	AccountAuth         bool
	ResponseControls    bool
	SelectableFormat    bool
	RequireBaseURL      bool
	settings            func(*Config) *ProviderConfig
}

var providerDefinitions = []ProviderDefinition{
	{
		ID: ProviderOpenAI, Name: "OpenAI", TabName: "OpenAI", PanelID: "openai", Monogram: "O", EnvPrefix: "OPENAI",
		DefaultBaseURL: "https://api.openai.com/v1", DefaultOutputTokens: defaultOpenAIMaxOutputTokens,
		Format: FormatResponses, EndpointKind: "Official OpenAI Responses API", AccountAuth: true, ResponseControls: true,
		settings: func(c *Config) *ProviderConfig { return &c.OpenAI },
	},
	{
		ID: ProviderOpenAICompatible, Name: "OpenAI-compatible", TabName: "OAI Compat.", PanelID: "compatible", Monogram: "C", EnvPrefix: "OPENAI_COMPATIBLE",
		DefaultOutputTokens: defaultCompatibleMaxOutputTokens,
		Format:              FormatResponses, EndpointKind: "Compatible Responses API", ResponseControls: true, SelectableFormat: true, RequireBaseURL: true,
		settings: func(c *Config) *ProviderConfig { return &c.Compatible },
	},
	{
		ID: ProviderAnthropic, Name: "Anthropic", TabName: "Anthropic", PanelID: "anthropic", Monogram: "A", EnvPrefix: "ANTHROPIC",
		DefaultBaseURL: "https://api.anthropic.com/v1", DefaultOutputTokens: defaultAnthropicMaxOutputTokens,
		Format: FormatMessages, EndpointKind: "Anthropic Messages API",
		settings: func(c *Config) *ProviderConfig { return &c.Anthropic },
	},
}

// ProviderDefinitions returns the supported routes in dashboard display order.
func ProviderDefinitions() []*ProviderDefinition {
	definitions := make([]*ProviderDefinition, 0, len(providerDefinitions))
	for i := range providerDefinitions {
		definition := providerDefinitions[i]
		definitions = append(definitions, &definition)
	}
	return definitions
}

// LookupProvider returns a route definition for its stable identifier.
func LookupProvider(id string) (*ProviderDefinition, bool) {
	for i := range providerDefinitions {
		if providerDefinitions[i].ID == id {
			definition := providerDefinitions[i]
			return &definition, true
		}
	}
	return nil, false
}

// ProviderIDs reports the supported identifiers for configuration diagnostics.
func ProviderIDs() string {
	ids := make([]string, 0, len(providerDefinitions))
	for i := range providerDefinitions {
		ids = append(ids, providerDefinitions[i].ID)
	}
	return strings.Join(ids, ", ")
}

// Settings returns the route's configuration within c.
func (d *ProviderDefinition) Settings(c *Config) *ProviderConfig {
	return d.settings(c)
}

// WireFormat resolves the explicit format, or the definition's default.
func (d *ProviderDefinition) WireFormat(settings *ProviderConfig) string {
	if d.SelectableFormat && strings.TrimSpace(settings.APIFormat) != "" {
		return strings.ToLower(strings.TrimSpace(settings.APIFormat))
	}
	return d.Format
}

// ProviderSettings returns one route's settings, or nil for an unknown identifier.
func (c *Config) ProviderSettings(id string) *ProviderConfig {
	definition, ok := LookupProvider(id)
	if !ok || c == nil {
		return nil
	}
	return definition.Settings(c)
}
