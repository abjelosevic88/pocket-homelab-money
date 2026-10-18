// Package ai talks to an OpenAI-compatible API: chat completions for the
// drafts, questions, stories and answers, and audio transcriptions for
// dictation. Nothing in the app depends on it; without a key every AI button
// stays hidden.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Defaults used when nothing is configured.
const (
	DefaultModel      = "gpt-5.4-mini"
	DefaultTranscribe = "gpt-4o-mini-transcribe"
)

// Client holds the endpoint and key.
type Client struct {
	BaseURL         string
	Key             string
	Model           string
	TranscribeModel string
	HTTP            *http.Client
}

// New creates a client.
func New(baseURL, key, model, transcribe string) *Client {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = DefaultModel
	}
	if transcribe == "" {
		transcribe = DefaultTranscribe
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, Model: model, TranscribeModel: transcribe, HTTP: &http.Client{Timeout: 150 * time.Second}}
}

// Configured reports whether a key is set.
func (c *Client) Configured() bool { return c != nil && c.Key != "" }

// Usage is what one call cost in tokens.
type Usage struct {
	Model            string `json:"model"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// reasoning reports whether the model takes a reasoning_effort (gpt-5*, o-series).
func reasoning(model string) bool {
	m := strings.ToLower(model)
	if strings.Contains(m, "chat-latest") {
		return false
	}
	return strings.HasPrefix(m, "gpt-5") || (len(m) > 1 && m[0] == 'o' && m[1] >= '1' && m[1] <= '9')
}

// Complete sends a system + user prompt and returns the assistant text.
// With jsonMode the model is asked for a single JSON object.
func (c *Client) Complete(ctx context.Context, system, user string, jsonMode bool) (string, Usage, error) {
	u := Usage{Model: c.Model}
	if !c.Configured() {
		return "", u, errors.New("AI is not configured")
	}
	body := map[string]any{
		"model":    c.Model,
		"messages": []message{{Role: "system", Content: system}, {Role: "user", Content: user}},
	}
	if jsonMode {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	if reasoning(c.Model) {
		body["reasoning_effort"] = "low"
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", u, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", u, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != http.StatusOK {
		return "", u, apiError(res.StatusCode, raw)
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", u, err
	}
	u.PromptTokens, u.CompletionTokens = out.Usage.Prompt, out.Usage.Completion
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", u, errors.New("AI returned no answer")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), u, nil
}

// JSON runs Complete in JSON mode and decodes into v.
func (c *Client) JSON(ctx context.Context, system, user string, v any) (Usage, error) {
	txt, u, err := c.Complete(ctx, system, user, true)
	if err != nil {
		return u, err
	}
	txt = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(txt), "```json"), "```")
	txt = strings.TrimSuffix(strings.TrimSpace(txt), "```")
	if err := json.Unmarshal([]byte(strings.TrimSpace(txt)), v); err != nil {
		return u, fmt.Errorf("AI answer was not valid JSON: %w", err)
	}
	return u, nil
}

// Transcribe turns recorded audio into text. lang is an ISO-639-1 hint or "".
func (c *Client) Transcribe(ctx context.Context, audio []byte, filename, lang string) (string, error) {
	if !c.Configured() {
		return "", errors.New("AI is not configured")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", c.TranscribeModel)
	if lang != "" {
		_ = mw.WriteField("language", lang)
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(audio); err != nil {
		return "", err
	}
	_ = mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/audio/transcriptions", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return "", apiError(res.StatusCode, raw)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Text), nil
}

// Models lists available model ids (connection test).
func (c *Client) Models(ctx context.Context) ([]string, error) {
	if !c.Configured() {
		return nil, errors.New("AI is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != http.StatusOK {
		return nil, apiError(res.StatusCode, raw)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, d := range out.Data {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

func apiError(status int, raw []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	if status == http.StatusUnauthorized {
		return errors.New("the API key was rejected (401)")
	}
	if e.Error.Message != "" {
		return fmt.Errorf("AI HTTP %d: %s", status, e.Error.Message)
	}
	return fmt.Errorf("AI HTTP %d", status)
}
