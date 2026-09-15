package openaimodel

import (
	"fmt"
	"slices"

	"github.com/openai/openai-go/v3/shared"
	"github.com/sylumi/agentkit/model"
)

// mapReasoning reads model capabilities directly and encodes one call's settings.
// The caller validates neutral settings before calling this function.
func mapReasoning(cfg *model.ReasoningConfig, info model.ModelInfo) (shared.ReasoningParam, error) {
	if cfg == nil || (cfg.Enabled == nil && cfg.Effort == "") {
		return shared.ReasoningParam{}, nil
	}

	supported := info.Capabilities.Reasoning
	if supported != nil && !*supported {
		if cfg.Enabled != nil && !*cfg.Enabled {
			return shared.ReasoningParam{}, nil
		}
		return shared.ReasoningParam{}, fmt.Errorf(
			"config.reasoning: reasoning is not supported",
		)
	}

	options := info.ReasoningOptions
	if cfg.Effort != "" {
		if options.Efforts != nil &&
			!slices.Contains(options.Efforts, cfg.Effort) {
			return shared.ReasoningParam{}, fmt.Errorf(
				"config.reasoning.effort: unsupported intensity %q; supported intensities: %v",
				cfg.Effort,
				options.Efforts,
			)
		}
		return shared.ReasoningParam{
			Effort: shared.ReasoningEffort(cfg.Effort),
		}, nil
	}

	if !*cfg.Enabled {
		if options.Toggle != nil && !*options.Toggle {
			return shared.ReasoningParam{}, fmt.Errorf(
				"config.reasoning.enabled: disabling reasoning is not supported",
			)
		}
		if options.Toggle == nil &&
			!slices.Contains(options.Efforts, "none") {
			return shared.ReasoningParam{}, fmt.Errorf(
				"config.reasoning.enabled: disabling reasoning is not declared by model metadata",
			)
		}
		return shared.ReasoningParam{Effort: "none"}, nil
	}

	return shared.ReasoningParam{}, fmt.Errorf(
		"config.reasoning.effort: set an explicit intensity to enable reasoning",
	)
}
