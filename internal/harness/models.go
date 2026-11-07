package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// AgentModel contains only display/selection data, never endpoints or credentials.
type AgentModel struct {
	ID, Name, Provider, DefaultEffort string
	Efforts                           []string
}

// ResolveEffort preserves compatible choices, then uses only an advertised
// default or the sole available value. Empty means inherit the CLI default.
func (m AgentModel) ResolveEffort(current string) string {
	if current != "" && slices.Contains(m.Efforts, current) {
		return current
	}
	if m.DefaultEffort != "" && slices.Contains(m.Efforts, m.DefaultEffort) {
		return m.DefaultEffort
	}
	if len(m.Efforts) == 1 {
		return m.Efforts[0]
	}
	return ""
}

// DiscoverModels uses the same read-only RPCs as the official CLI SDKs.
// No session or turn is created. Each probe owns and reaps its process tree.
func DiscoverModels(ctx context.Context, driver, binary, cwd string) ([]AgentModel, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := []string{"--mode", "rpc", "--no-session", "--no-extensions"}
	request := map[string]any{"id": "models", "type": "get_available_models"}
	if driver == "droid" {
		args = []string{"exec", "--input-format", "stream-jsonrpc", "--output-format", "stream-jsonrpc"}
		request = map[string]any{"id": "models", "jsonrpc": "2.0", "type": "request", "factoryApiVersion": "1.0.0", "factoryProtocolVersion": "1.269.0", "method": "droid.list_models", "params": map[string]any{}}
	} else if driver != "pi" {
		return nil, fmt.Errorf("unsupported agent: %s", driver)
	}
	process, err := startRPCProcess(ctx, binary, cwd, args)
	if err != nil {
		return nil, err
	}
	defer process.close()
	if err = process.input.Encode(request); err != nil {
		return nil, err
	}
	scanner := process.output
	for scanner.Scan() {
		var envelope struct {
			ID           string
			Success      bool
			Data, Result json.RawMessage
			Error        json.RawMessage
		}
		if err = json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			return nil, fmt.Errorf("invalid model catalog response")
		}
		if envelope.ID != "models" {
			continue
		}
		if len(envelope.Error) > 0 && string(envelope.Error) != "null" || driver == "pi" && !envelope.Success {
			// Do not surface raw process errors: custom provider diagnostics may contain secrets.
			return nil, fmt.Errorf("model discovery failed; check CLI login/configuration and update the CLI")
		}
		data := envelope.Data
		if driver == "droid" {
			data = envelope.Result
		}
		return parseModelCatalog(driver, data)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("CLI exited without a model catalog; update the CLI and retry")
}
func parseModelCatalog(driver string, data []byte) ([]AgentModel, error) {
	var catalog struct {
		Models []struct {
			ID, Name, Provider, DisplayName, ModelProvider, DefaultReasoningEffort string
			SupportedReasoningEfforts                                              []string
			ThinkingLevelMap                                                       map[string]json.RawMessage
			Reasoning, Disabled                                                    bool
		}
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("invalid model catalog")
	}
	var models []AgentModel
	seen := map[string]bool{}
	for _, raw := range catalog.Models {
		if raw.ID == "" || raw.Disabled {
			continue
		}
		m := AgentModel{ID: raw.ID, Name: raw.DisplayName, Provider: raw.ModelProvider, Efforts: raw.SupportedReasoningEfforts, DefaultEffort: raw.DefaultReasoningEffort}
		if driver == "pi" {
			if raw.Provider == "" {
				continue
			}
			m.ID = raw.Provider + "/" + raw.ID
			m.Name = raw.Name
			m.Provider = raw.Provider
			m.Efforts = nil
			if raw.Reasoning {
				// Match Pi's getSupportedThinkingLevels: null excludes a level;
				// absent standard entries inherit provider defaults, while extended
				// levels require an explicit non-null mapping. Never invent off.
				for _, effort := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
					mapped, present := raw.ThinkingLevelMap[effort]
					if strings.TrimSpace(string(mapped)) == "null" || !present && (effort == "xhigh" || effort == "max") {
						continue
					}
					m.Efforts = append(m.Efforts, effort)
				}
			} else {
				m.Efforts = []string{"off"}
			}
		}
		levels := make([]string, 0, len(m.Efforts))
		for _, effort := range m.Efforts {
			if effort != "" && !slices.Contains(levels, effort) {
				levels = append(levels, effort)
			}
		}
		m.Efforts = levels
		if !slices.Contains(m.Efforts, m.DefaultEffort) {
			m.DefaultEffort = ""
		}
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		if strings.TrimSpace(m.Name) == "" {
			m.Name = m.ID
		}
		models = append(models, m)
	}
	return models, nil
}
