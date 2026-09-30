package service

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/tidwall/gjson"
)

// Omitted effort keeps the official medium default; unsupported explicit efforts
// fail before a model request rather than silently changing the user's request.
func validateGPT61SolReasoning(body []byte, model string) error {
	if !openai.IsGPT61SolModelSpelling(model) {
		return nil
	}
	for _, path := range []string{"reasoning.effort", "reasoning_effort", "output_config.effort"} {
		effort := gjson.GetBytes(body, path)
		if !effort.Exists() || effort.Type == gjson.Null {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(effort.String())) {
		case "low", "medium", "high", "xhigh", "max":
		default:
			return fmt.Errorf("gpt-6.1-sol supports reasoning effort low, medium, high, xhigh or max; %s is unsupported", path)
		}
	}
	return nil
}

func hasGPT61SolChatTools(body []byte) bool {
	if len(gjson.GetBytes(body, "tools").Array()) > 0 || len(gjson.GetBytes(body, "functions").Array()) > 0 {
		return true
	}
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		if message.Get("role").String() == "tool" || message.Get("role").String() == "function" || len(message.Get("tool_calls").Array()) > 0 || message.Get("function_call").IsObject() {
			return true
		}
	}
	return false
}
