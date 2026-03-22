package interpreter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/codong-lang/codong/stdlib/codongerror"
)

// LlmModuleObject is the singleton `llm` module.
type LlmModuleObject struct{}

func (l *LlmModuleObject) Type() string    { return "module" }
func (l *LlmModuleObject) Inspect() string { return "<module:llm>" }

var llmModuleSingleton = &LlmModuleObject{}

// Provider config resolved from args → env → .codong.env
type llmConfig struct {
	provider string // "openai", "anthropic", "google", "ollama", "custom"
	apiKey   string
	baseURL  string
	model    string
}

func resolveLLMConfig(args []Object, named map[string]Object) llmConfig {
	cfg := llmConfig{
		provider: "openai",
		model:    "gpt-4o",
		baseURL:  "https://api.openai.com/v1",
	}

	// Extract from named args
	if m, ok := named["model"]; ok {
		if s, ok := m.(*StringObject); ok {
			cfg.model = s.Value
			cfg.provider = detectProvider(s.Value)
		}
	}
	if k, ok := named["api_key"]; ok {
		if s, ok := k.(*StringObject); ok {
			cfg.apiKey = s.Value
		}
	}
	if u, ok := named["base_url"]; ok {
		if s, ok := u.(*StringObject); ok {
			cfg.baseURL = s.Value
			cfg.provider = "custom"
		}
	}

	// Positional: first arg is often model string
	if len(args) > 0 {
		if s, ok := args[0].(*StringObject); ok {
			if isModelName(s.Value) {
				cfg.model = s.Value
				cfg.provider = detectProvider(s.Value)
			}
		}
	}

	// Fall back to env vars if no API key
	if cfg.apiKey == "" {
		cfg.apiKey = resolveAPIKey(cfg.provider)
	}

	// Set base URL per provider
	if cfg.baseURL == "https://api.openai.com/v1" {
		switch cfg.provider {
		case "anthropic":
			cfg.baseURL = "https://api.anthropic.com/v1"
		case "google":
			cfg.baseURL = "https://generativelanguage.googleapis.com/v1beta"
		case "ollama":
			cfg.baseURL = "http://localhost:11434/api"
		}
	}

	return cfg
}

func detectProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3"):
		return "openai"
	case strings.HasPrefix(model, "claude"):
		return "anthropic"
	case strings.HasPrefix(model, "gemini"):
		return "google"
	case strings.Contains(model, ":") || strings.HasPrefix(model, "llama") || strings.HasPrefix(model, "mistral") || strings.HasPrefix(model, "qwen"):
		return "ollama"
	default:
		return "openai"
	}
}

func isModelName(s string) bool {
	prefixes := []string{"gpt-", "o1", "o3", "claude", "gemini", "llama", "mistral", "qwen", "deepseek"}
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return strings.Contains(s, "/") || strings.Contains(s, ":")
}

func resolveAPIKey(provider string) string {
	// Priority: env var
	switch provider {
	case "openai":
		if k := os.Getenv("OPENAI_API_KEY"); k != "" {
			return k
		}
	case "anthropic":
		if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
			return k
		}
	case "google":
		if k := os.Getenv("GOOGLE_API_KEY"); k != "" {
			return k
		}
	}
	// Generic fallback
	if k := os.Getenv("LLM_API_KEY"); k != "" {
		return k
	}
	return ""
}

// evalLlmModuleMethod dispatches llm.xxx() calls.
func (interp *Interpreter) evalLlmModuleMethod(method string) Object {
	return &BuiltinFunction{
		Name: "llm." + method,
		Fn: func(i *Interpreter, args ...Object) Object {
			// Extract named args from trailing MapObject
			named := map[string]Object{}
			if len(args) > 0 {
				if m, ok := args[len(args)-1].(*MapObject); ok {
					for k, v := range m.Entries {
						named[k] = v
					}
				}
			}

			switch method {
			case "ask":
				return i.llmAsk(args, named)
			case "chat":
				return i.llmChat(args, named)
			case "embed":
				return i.llmEmbed(args, named)
			case "embed_batch":
				return i.llmEmbedBatch(args, named)
			case "classify":
				return i.llmClassify(args, named)
			case "extract":
				return i.llmExtract(args, named)
			case "summarize":
				return i.llmSummarize(args, named)
			case "translate":
				return i.llmTranslate(args, named)
			case "count_tokens":
				return i.llmCountTokens(args, named)
			default:
				return newRuntimeError(codongerror.E4001_LLM_ERROR,
					fmt.Sprintf("unknown llm method: %s", method), "")
			}
		},
	}
}

// llmAsk performs a single prompt → response call.
// llm.ask("What is 2+2?", model: "gpt-4o")
func (i *Interpreter) llmAsk(args []Object, named map[string]Object) Object {
	cfg := resolveLLMConfig(args, named)
	if cfg.apiKey == "" && cfg.provider != "ollama" {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E4005_API_KEY_MISSING,
			fmt.Sprintf("no API key for %s", cfg.provider),
			codongerror.WithFix("set api_key: llm.ask(prompt, model: \"gpt-4o\", api_key: \"sk-...\") or export OPENAI_API_KEY"),
		)}
	}

	// Find the prompt (first non-model string arg)
	prompt := ""
	for _, a := range args {
		if s, ok := a.(*StringObject); ok && !isModelName(s.Value) {
			prompt = s.Value
			break
		}
	}
	if p, ok := named["prompt"]; ok {
		if s, ok := p.(*StringObject); ok {
			prompt = s.Value
		}
	}
	if prompt == "" {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"llm.ask requires a prompt string",
			"llm.ask(\"What is 2+2?\", model: \"gpt-4o\")")
	}

	// System prompt
	system := ""
	if sp, ok := named["system"]; ok {
		if s, ok := sp.(*StringObject); ok {
			system = s.Value
		}
	}

	// Temperature
	temperature := 0.7
	if t, ok := named["temperature"]; ok {
		if n, ok := t.(*NumberObject); ok {
			temperature = n.Value
		}
	}

	// Max tokens
	maxTokens := 4096
	if mt, ok := named["max_tokens"]; ok {
		if n, ok := mt.(*NumberObject); ok {
			maxTokens = int(n.Value)
		}
	}

	return i.callLLM(cfg, prompt, system, temperature, maxTokens)
}

// llmChat performs a multi-turn conversation.
// llm.chat([{role: "user", content: "hi"}], model: "gpt-4o")
func (i *Interpreter) llmChat(args []Object, named map[string]Object) Object {
	cfg := resolveLLMConfig(args, named)
	if cfg.apiKey == "" && cfg.provider != "ollama" {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E4005_API_KEY_MISSING,
			fmt.Sprintf("no API key for %s", cfg.provider),
		)}
	}

	// Find messages list
	var messages *ListObject
	for _, a := range args {
		if l, ok := a.(*ListObject); ok {
			messages = l
			break
		}
	}
	if messages == nil {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"llm.chat requires a messages list",
			"llm.chat([{role: \"user\", content: \"hi\"}], model: \"gpt-4o\")")
	}

	temperature := 0.7
	if t, ok := named["temperature"]; ok {
		if n, ok := t.(*NumberObject); ok {
			temperature = n.Value
		}
	}
	maxTokens := 4096
	if mt, ok := named["max_tokens"]; ok {
		if n, ok := mt.(*NumberObject); ok {
			maxTokens = int(n.Value)
		}
	}

	return i.callLLMChat(cfg, messages, temperature, maxTokens)
}

// llmEmbed generates embeddings for a single text.
func (i *Interpreter) llmEmbed(args []Object, named map[string]Object) Object {
	cfg := resolveLLMConfig(args, named)
	if cfg.model == "gpt-4o" {
		cfg.model = "text-embedding-3-small" // default embedding model
	}
	if cfg.apiKey == "" {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E4005_API_KEY_MISSING, "no API key for embeddings")}
	}

	text := ""
	for _, a := range args {
		if s, ok := a.(*StringObject); ok && !isModelName(s.Value) {
			text = s.Value
			break
		}
	}

	return i.callEmbedding(cfg, []string{text})
}

// llmEmbedBatch generates embeddings for multiple texts.
func (i *Interpreter) llmEmbedBatch(args []Object, named map[string]Object) Object {
	cfg := resolveLLMConfig(args, named)
	if cfg.model == "gpt-4o" {
		cfg.model = "text-embedding-3-small"
	}
	if cfg.apiKey == "" {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E4005_API_KEY_MISSING, "no API key for embeddings")}
	}

	var texts []string
	for _, a := range args {
		if l, ok := a.(*ListObject); ok {
			for _, el := range l.Elements {
				if s, ok := el.(*StringObject); ok {
					texts = append(texts, s.Value)
				}
			}
			break
		}
	}

	return i.callEmbedding(cfg, texts)
}

// Convenience wrappers using llm.ask with system prompts
func (i *Interpreter) llmClassify(args []Object, named map[string]Object) Object {
	// Inject classification system prompt
	categories := "unknown"
	if c, ok := named["categories"]; ok {
		if l, ok := c.(*ListObject); ok {
			parts := make([]string, len(l.Elements))
			for idx, el := range l.Elements {
				parts[idx] = objectToString(el)
			}
			categories = strings.Join(parts, ", ")
		}
	}
	named["system"] = &StringObject{Value: fmt.Sprintf(
		"Classify the following text into exactly one of these categories: %s. Respond with only the category name, nothing else.", categories)}
	return i.llmAsk(args, named)
}

func (i *Interpreter) llmExtract(args []Object, named map[string]Object) Object {
	schema := "{}"
	if s, ok := named["schema"]; ok {
		goVal := objectToGoValue(s)
		jsonBytes, _ := json.Marshal(goVal)
		schema = string(jsonBytes)
	}
	named["system"] = &StringObject{Value: fmt.Sprintf(
		"Extract structured data from the text. Return a valid JSON object matching this schema: %s. Return ONLY the JSON, no explanation.", schema)}
	return i.llmAsk(args, named)
}

func (i *Interpreter) llmSummarize(args []Object, named map[string]Object) Object {
	maxLen := "3 sentences"
	if ml, ok := named["max_length"]; ok {
		maxLen = objectToString(ml)
	}
	named["system"] = &StringObject{Value: fmt.Sprintf(
		"Summarize the following text in %s. Be concise and capture the key points.", maxLen)}
	return i.llmAsk(args, named)
}

func (i *Interpreter) llmTranslate(args []Object, named map[string]Object) Object {
	target := "English"
	if t, ok := named["to"]; ok {
		target = objectToString(t)
	}
	named["system"] = &StringObject{Value: fmt.Sprintf(
		"Translate the following text to %s. Return only the translation, nothing else.", target)}
	return i.llmAsk(args, named)
}

func (i *Interpreter) llmCountTokens(args []Object, named map[string]Object) Object {
	text := ""
	for _, a := range args {
		if s, ok := a.(*StringObject); ok {
			text = s.Value
			break
		}
	}
	// Rough estimation: ~4 chars per token for English
	estimate := float64(len(text)) / 4.0
	return &NumberObject{Value: estimate}
}

// --- API Callers ---

func (i *Interpreter) callLLM(cfg llmConfig, prompt, system string, temperature float64, maxTokens int) Object {
	switch cfg.provider {
	case "openai", "custom":
		return i.callOpenAI(cfg, prompt, system, temperature, maxTokens)
	case "anthropic":
		return i.callAnthropic(cfg, prompt, system, temperature, maxTokens)
	case "ollama":
		return i.callOllama(cfg, prompt, system, temperature)
	case "google":
		return i.callGoogle(cfg, prompt, system, temperature)
	default:
		return i.callOpenAI(cfg, prompt, system, temperature, maxTokens)
	}
}

func (i *Interpreter) callLLMChat(cfg llmConfig, messages *ListObject, temperature float64, maxTokens int) Object {
	// Convert Codong messages to API format
	msgs := make([]map[string]string, 0)
	for _, m := range messages.Elements {
		if mo, ok := m.(*MapObject); ok {
			msg := map[string]string{}
			if r, ok := mo.Entries["role"]; ok {
				msg["role"] = objectToString(r)
			}
			if c, ok := mo.Entries["content"]; ok {
				msg["content"] = objectToString(c)
			}
			msgs = append(msgs, msg)
		}
	}

	switch cfg.provider {
	case "anthropic":
		return i.callAnthropicChat(cfg, msgs, temperature, maxTokens)
	default:
		return i.callOpenAIChat(cfg, msgs, temperature, maxTokens)
	}
}

// --- OpenAI Compatible ---

func (i *Interpreter) callOpenAI(cfg llmConfig, prompt, system string, temperature float64, maxTokens int) Object {
	messages := []map[string]string{}
	if system != "" {
		messages = append(messages, map[string]string{"role": "system", "content": system})
	}
	messages = append(messages, map[string]string{"role": "user", "content": prompt})

	body := map[string]interface{}{
		"model":       cfg.model,
		"messages":    messages,
		"temperature": temperature,
		"max_tokens":  maxTokens,
	}

	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", cfg.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.apiKey)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E4001_LLM_ERROR, "request failed: "+err.Error())}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return i.handleLLMError(resp.StatusCode, respBody, cfg.provider)
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	// Extract content from response
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if message, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := message["content"].(string); ok {
					return &StringObject{Value: content}
				}
			}
		}
	}
	return &StringObject{Value: string(respBody)}
}

func (i *Interpreter) callOpenAIChat(cfg llmConfig, messages []map[string]string, temperature float64, maxTokens int) Object {
	body := map[string]interface{}{
		"model":       cfg.model,
		"messages":    messages,
		"temperature": temperature,
		"max_tokens":  maxTokens,
	}

	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", cfg.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.apiKey)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E4001_LLM_ERROR, "request failed: "+err.Error())}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return i.handleLLMError(resp.StatusCode, respBody, cfg.provider)
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	// Return full response as MapObject
	return goValueToObject(result)
}

// --- Anthropic ---

func (i *Interpreter) callAnthropic(cfg llmConfig, prompt, system string, temperature float64, maxTokens int) Object {
	body := map[string]interface{}{
		"model":      cfg.model,
		"max_tokens": maxTokens,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	}
	if system != "" {
		body["system"] = system
	}
	if temperature != 0.7 {
		body["temperature"] = temperature
	}

	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", cfg.baseURL+"/messages", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E4001_LLM_ERROR, "request failed: "+err.Error())}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return i.handleLLMError(resp.StatusCode, respBody, cfg.provider)
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	// Extract text from Anthropic response
	if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
		if block, ok := content[0].(map[string]interface{}); ok {
			if text, ok := block["text"].(string); ok {
				return &StringObject{Value: text}
			}
		}
	}
	return &StringObject{Value: string(respBody)}
}

func (i *Interpreter) callAnthropicChat(cfg llmConfig, messages []map[string]string, temperature float64, maxTokens int) Object {
	// Separate system message
	system := ""
	apiMessages := make([]map[string]string, 0)
	for _, m := range messages {
		if m["role"] == "system" {
			system = m["content"]
		} else {
			apiMessages = append(apiMessages, m)
		}
	}

	body := map[string]interface{}{
		"model":      cfg.model,
		"max_tokens": maxTokens,
		"messages":   apiMessages,
	}
	if system != "" {
		body["system"] = system
	}

	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", cfg.baseURL+"/messages", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E4001_LLM_ERROR, "request failed: "+err.Error())}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return i.handleLLMError(resp.StatusCode, respBody, cfg.provider)
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	return goValueToObject(result)
}

// --- Ollama ---

func (i *Interpreter) callOllama(cfg llmConfig, prompt, system string, temperature float64) Object {
	body := map[string]interface{}{
		"model":  cfg.model,
		"prompt": prompt,
		"stream": false,
	}
	if system != "" {
		body["system"] = system
	}
	if temperature != 0.7 {
		body["options"] = map[string]interface{}{"temperature": temperature}
	}

	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", cfg.baseURL+"/generate", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E4001_LLM_ERROR, "Ollama request failed: "+err.Error(),
			codongerror.WithFix("ensure Ollama is running: ollama serve"),
		)}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	if response, ok := result["response"].(string); ok {
		return &StringObject{Value: response}
	}
	return &StringObject{Value: string(respBody)}
}

// --- Google Gemini ---

func (i *Interpreter) callGoogle(cfg llmConfig, prompt, system string, temperature float64) Object {
	fullPrompt := prompt
	if system != "" {
		fullPrompt = system + "\n\n" + prompt
	}

	body := map[string]interface{}{
		"contents": []map[string]interface{}{
			{"parts": []map[string]string{{"text": fullPrompt}}},
		},
	}
	if temperature != 0.7 {
		body["generationConfig"] = map[string]interface{}{"temperature": temperature}
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", cfg.baseURL, cfg.model, cfg.apiKey)
	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E4001_LLM_ERROR, "Gemini request failed: "+err.Error())}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return i.handleLLMError(resp.StatusCode, respBody, "google")
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	if candidates, ok := result["candidates"].([]interface{}); ok && len(candidates) > 0 {
		if c, ok := candidates[0].(map[string]interface{}); ok {
			if content, ok := c["content"].(map[string]interface{}); ok {
				if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
					if p, ok := parts[0].(map[string]interface{}); ok {
						if text, ok := p["text"].(string); ok {
							return &StringObject{Value: text}
						}
					}
				}
			}
		}
	}
	return &StringObject{Value: string(respBody)}
}

// --- Embeddings ---

func (i *Interpreter) callEmbedding(cfg llmConfig, texts []string) Object {
	body := map[string]interface{}{
		"model": cfg.model,
		"input": texts,
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", cfg.baseURL+"/embeddings", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E4001_LLM_ERROR, "embedding request failed: "+err.Error())}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return i.handleLLMError(resp.StatusCode, respBody, cfg.provider)
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	// Extract embeddings
	if data, ok := result["data"].([]interface{}); ok {
		embeddings := make([]Object, len(data))
		for idx, d := range data {
			if dm, ok := d.(map[string]interface{}); ok {
				if emb, ok := dm["embedding"].([]interface{}); ok {
					nums := make([]Object, len(emb))
					for j, n := range emb {
						if f, ok := n.(float64); ok {
							nums[j] = &NumberObject{Value: f}
						}
					}
					embeddings[idx] = &ListObject{Elements: nums}
				}
			}
		}
		if len(texts) == 1 && len(embeddings) == 1 {
			return embeddings[0] // single text → single embedding
		}
		return &ListObject{Elements: embeddings}
	}
	return goValueToObject(result)
}

// --- Error Handling ---

func (i *Interpreter) handleLLMError(statusCode int, body []byte, provider string) Object {
	var errData map[string]interface{}
	json.Unmarshal(body, &errData)

	msg := string(body)
	code := codongerror.E4001_LLM_ERROR
	fix := ""

	switch statusCode {
	case 401:
		code = codongerror.E4005_API_KEY_MISSING
		msg = fmt.Sprintf("%s: invalid API key", provider)
		fix = "check your API key"
	case 429:
		code = codongerror.E4002_RATE_LIMITED
		msg = fmt.Sprintf("%s: rate limited", provider)
		fix = "wait and retry, or upgrade your plan"
	case 404:
		code = codongerror.E4004_MODEL_NOT_FOUND
		msg = fmt.Sprintf("%s: model not found", provider)
		fix = "check model name"
	}

	// Try to extract error message from response
	if e, ok := errData["error"].(map[string]interface{}); ok {
		if m, ok := e["message"].(string); ok {
			msg = m
		}
	}

	return &ErrorObject{IsRuntime: false, Error: codongerror.New(
		code, msg, codongerror.WithFix(fix),
		codongerror.WithRetry(statusCode == 429 || statusCode == 500 || statusCode == 503),
	)}
}

// helper
func objectToString(obj Object) string {
	if s, ok := obj.(*StringObject); ok {
		return s.Value
	}
	return obj.Inspect()
}
