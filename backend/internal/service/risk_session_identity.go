package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// RiskSessionBlockKeys identifies an authenticated user's rejected conversation.
// The exact transcript and (after an assistant turn) the context immediately
// before the latest user turn allow retries and rewrites to match. Keys contain
// only hashes: API keys, IPs, model selection and request IDs are not identity.
// Client-supplied history is not authenticated provenance; without a stable
// session ID, an earlier rewind or omitted/compacted history cannot be inferred.
func RiskSessionBlockKeys(userID int64, c *gin.Context, body []byte) []string {
	return riskSessionIdentityKeys(userID, c, body, false)
}

// RiskSessionLookupKeys includes every semantic transcript prefix. It must not
// discard older prefixes: padding a blocked transcript must not bypass a block.
// Work is linear in the bounded inbound body; callers should batch DB lookups.
func RiskSessionLookupKeys(userID int64, c *gin.Context, body []byte) []string {
	return riskSessionIdentityKeys(userID, c, body, true)
}

func riskSessionIdentityKeys(userID int64, c *gin.Context, body []byte, lookup bool) []string {
	if userID <= 0 {
		return nil
	}
	var root gjson.Result
	if gjson.ValidBytes(body) {
		root = openAIRequestPayloadView(body)
	}
	var keys []string
	if id := riskSessionExplicitID(c, root); id != "" {
		h := riskSessionIdentityHash(userID, "session")
		riskSessionHashPart(h, id)
		keys = append(keys, hex.EncodeToString(h.Sum(nil)))
	}
	if !root.IsObject() {
		return keys
	}

	h := riskSessionIdentityHash(userID, "transcript")
	// Root-level system context is equivalent across the supported protocols.
	for _, field := range []string{"instructions", "system", "systemInstruction", "system_instruction"} {
		v := root.Get(field)
		if !v.Exists() {
			continue
		}
		if v.IsObject() && v.Get("parts").IsArray() {
			v = v.Get("parts")
		}
		if content := riskSessionCanonicalContent(v); content != "" {
			riskSessionHashPart(h, `{"content":`+content+`,"role":"system"}`)
		}
	}

	var sequence gjson.Result
	for _, field := range []string{"messages", "input", "contents"} {
		v := root.Get(field)
		if v.IsArray() {
			sequence = v
			break
		}
	}
	lastKey, preLatestUser := "", ""
	hasModelHistory, hasConversationContent := false, false
	appendItem := func(item gjson.Result) {
		canonical, userTurn, modelTurn, conversation := riskSessionCanonicalItem(item)
		if canonical == "" {
			return
		}
		if userTurn && hasModelHistory && lastKey != "" {
			preLatestUser = lastKey
		}
		riskSessionHashPart(h, canonical)
		hasConversationContent = hasConversationContent || conversation
		if hasConversationContent {
			lastKey = hex.EncodeToString(h.Sum(nil))
			if lookup {
				keys = append(keys, lastKey)
			}
		}
		// The model role is an entropy heuristic, not proof of provenance:
		// client-provided few-shot examples may also carry an assistant role.
		hasModelHistory = hasModelHistory || modelTurn
	}
	if sequence.IsArray() {
		sequence.ForEach(func(_, item gjson.Result) bool {
			appendItem(item)
			return true
		})
	} else if input := root.Get("input"); input.Type == gjson.String {
		appendItem(input)
	} else if prompt := root.Get("prompt"); prompt.Exists() && strings.TrimSpace(prompt.String()) != "" {
		// Image/video requests have no history. Include media and masks as well
		// as the prompt so unrelated edits of different images do not collide.
		media := make(map[string]json.RawMessage)
		for _, field := range []string{"prompt", "image", "images", "image_url", "input_image", "mask", "video", "video_url", "audio", "input_audio"} {
			if v := root.Get(field); v.Exists() {
				media[field] = json.RawMessage(v.Raw)
			}
		}
		b, _ := json.Marshal(media)
		riskSessionHashPart(h, "media")
		riskSessionHashPart(h, normalizeCompatSeedJSON(b))
		lastKey = hex.EncodeToString(h.Sum(nil))
		if lookup {
			keys = append(keys, lastKey)
		}
	}
	if !lookup && lastKey != "" {
		keys = append(keys, lastKey)
		if preLatestUser != "" && preLatestUser != lastKey {
			keys = append(keys, preLatestUser)
		}
	}
	return keys
}

func riskSessionIdentityHash(userID int64, kind string) hash.Hash {
	h := sha256.New()
	riskSessionHashPart(h, "permanent-risk-session:v1")
	riskSessionHashPart(h, strconv.FormatInt(userID, 10))
	riskSessionHashPart(h, kind)
	return h
}

// Length framing prevents adversarial content from imitating item boundaries.
func riskSessionHashPart(h hash.Hash, part string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(part)))
	_, _ = h.Write(n[:])
	_, _ = h.Write([]byte(part))
}

func riskSessionExplicitID(c *gin.Context, root gjson.Result) string {
	if c != nil && c.Request != nil {
		if id := explicitOpenAIHeaderSessionID(c); id != "" {
			return id
		}
	}
	// Prefer an explicit conversation ID over a prompt-cache hint, which some
	// clients reuse for caching. Do not guess chains from previous_response_id.
	for _, path := range []string{"conversation_id", "session_id", "conversation.id", "conversation", "prompt_cache_key"} {
		v := root.Get(path)
		if v.Type == gjson.String {
			if id := strings.TrimSpace(v.String()); id != "" {
				return id
			}
		}
	}
	return ""
}

func riskSessionCanonicalItem(item gjson.Result) (canonical string, userTurn, modelTurn, conversation bool) {
	if item.Type == gjson.String {
		content := riskSessionCanonicalContent(item)
		if content == "" {
			return "", false, false, false
		}
		return `{"content":` + content + `,"role":"user"}`, true, false, true
	}
	if !item.IsObject() {
		return "", false, false, false
	}
	role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
	switch role {
	case "human":
		role = "user"
	case "model":
		role = "assistant"
	}
	typ := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
	if role == "" {
		switch typ {
		case "input_text", "input_image", "input_audio":
			role = "user"
		case "output_text":
			role = "assistant"
		}
	}
	content := item.Get("content")
	if !content.Exists() {
		content = item.Get("parts")
	}
	if !content.Exists() && (typ == "input_text" || typ == "output_text" || typ == "input_image" || typ == "input_audio") {
		content = item
	}
	parts := make(map[string]json.RawMessage)
	hasSemanticContent := false
	if role != "" {
		parts["role"], _ = json.Marshal(role)
	}
	if normalized := riskSessionCanonicalContent(content); normalized != "" {
		parts["content"] = json.RawMessage(normalized)
		hasSemanticContent = true
	}
	// Preserve tool data and model-visible fields, ignoring generated message
	// IDs/status/annotations and transport-only metadata/cache-control hints.
	for _, field := range []string{"tool_calls", "function_call", "tool_call_id", "call_id", "name", "arguments", "input", "output", "refusal", "reasoning_content", "summary", "encrypted_content"} {
		if v := item.Get(field); v.Exists() {
			parts[field] = json.RawMessage(normalizeCompatSeedJSON(json.RawMessage(v.Raw)))
			hasSemanticContent = true
		}
	}
	if typ != "" && typ != "message" && typ != "input_text" && typ != "output_text" && typ != "input_image" && typ != "input_audio" {
		parts["type"], _ = json.Marshal(typ)
	}
	// A role/type-only template contributes no content or usable identity.
	if !hasSemanticContent {
		return "", false, false, false
	}
	b, _ := json.Marshal(parts)
	userTurn = role == "user" && riskSessionHasUserContent(content)
	modelTurn = role == "assistant"
	switch typ {
	case "function_call", "tool_call", "custom_tool_call", "computer_call", "reasoning":
		modelTurn = true
	}
	conversation = role != "system" && role != "developer"
	return string(b), userTurn, modelTurn, conversation
}

func riskSessionCanonicalContent(content gjson.Result) string {
	if !content.Exists() || content.Type == gjson.Null {
		return ""
	}
	var blocks []json.RawMessage
	appendBlock := func(block gjson.Result) {
		if block.Type == gjson.String {
			if strings.TrimSpace(block.String()) == "" {
				return
			}
			b, _ := json.Marshal(map[string]string{"type": "text", "text": block.String()})
			blocks = append(blocks, b)
			return
		}
		if !block.IsObject() {
			return
		}
		typ := strings.ToLower(strings.TrimSpace(block.Get("type").String()))
		if typ == "text" || typ == "input_text" || typ == "output_text" || (typ == "" && block.Get("text").Type == gjson.String) {
			text := block.Get("text")
			if text.Type != gjson.String || strings.TrimSpace(text.String()) == "" {
				return
			}
			b, _ := json.Marshal(map[string]string{"type": "text", "text": text.String()})
			blocks = append(blocks, b)
			return
		}
		fields := make(map[string]json.RawMessage)
		block.ForEach(func(k, v gjson.Result) bool {
			switch k.String() {
			case "cache_control", "annotations":
				return true
			}
			fields[k.String()] = json.RawMessage(v.Raw)
			return true
		})
		if len(fields) == 0 || (len(fields) == 1 && fields["type"] != nil) {
			return
		}
		b, _ := json.Marshal(fields)
		blocks = append(blocks, json.RawMessage(normalizeCompatSeedJSON(b)))
	}
	if content.IsArray() {
		content.ForEach(func(_, block gjson.Result) bool {
			appendBlock(block)
			return true
		})
	} else {
		appendBlock(content)
	}
	if len(blocks) == 0 {
		return ""
	}
	b, _ := json.Marshal(blocks)
	return string(b)
}

func riskSessionHasUserContent(content gjson.Result) bool {
	if !content.IsArray() {
		return riskSessionCanonicalContent(content) != ""
	}
	hasUserContent := false
	content.ForEach(func(_, block gjson.Result) bool {
		switch strings.ToLower(strings.TrimSpace(block.Get("type").String())) {
		case "tool_result", "function_call_output", "custom_tool_call_output", "computer_call_output":
			return true
		}
		if block.Get("functionResponse").Exists() || block.Get("function_response").Exists() {
			return true
		}
		hasUserContent = riskSessionCanonicalContent(block) != ""
		return !hasUserContent
	})
	return hasUserContent
}
