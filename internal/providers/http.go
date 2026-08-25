package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/logging"
)

// IsContextOverflow classifies provider input-size rejections without binding
// the agent to any provider's private error type.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	var failure *ProviderFailure
	if errors.As(err, &failure) && failure.Code == FailureContextWindowExceeded {
		return true
	}
	var statusError *httpStatusError
	if errors.As(err, &statusError) && statusError.code != http.StatusBadRequest && statusError.code != http.StatusRequestEntityTooLarge {
		return false
	}
	if statusError != nil && statusError.code == http.StatusRequestEntityTooLarge {
		return true
	}
	message := strings.ToLower(err.Error())
	if statusError != nil {
		message += " " + strings.ToLower(statusError.body)
	}
	size := strings.Contains(message, "context") || strings.Contains(message, "input") || strings.Contains(message, "prompt") || strings.Contains(message, "context_length")
	limit := strings.Contains(message, "too long") || strings.Contains(message, "maximum") || strings.Contains(message, "limit") || strings.Contains(message, "exceed")
	return size && limit
}

func isUnsupportedStructuredOutput(err error) bool {
	if err == nil {
		return false
	}
	code := 0
	var status *httpStatusError
	if errors.As(err, &status) {
		code = status.code
	}
	if geminiCode, _, _, ok := GeminiAPIError(err); ok {
		code = geminiCode
	}
	if code != http.StatusBadRequest && code != http.StatusUnprocessableEntity {
		return false
	}
	message := strings.ToLower(err.Error())
	mentionsFormat := strings.Contains(message, "response_format") || strings.Contains(message, "json_schema") || strings.Contains(message, "structured output") || strings.Contains(message, "output_config") || strings.Contains(message, "response schema") || strings.Contains(message, "schema")
	unsupported := strings.Contains(message, "unsupported") || strings.Contains(message, "not support") || strings.Contains(message, "not available") || strings.Contains(message, "unknown") || strings.Contains(message, "extra_forbidden") || strings.Contains(message, "invalid") || strings.Contains(message, "not permitted")
	return mentionsFormat && unsupported
}

const maxResponseBytes = 10 << 20

type httpStatusError struct {
	code       int
	status     string
	body       string
	retryAfter time.Duration
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("provider API %s: %s", e.status, e.body)
}

func (e *httpStatusError) ErrorClass() string {
	return string(e.FailureCode())
}

func (e *httpStatusError) FailureCode() FailureCode {
	switch {
	case e.code == http.StatusUnauthorized || e.code == http.StatusForbidden:
		return FailureAuth
	case e.code == http.StatusTooManyRequests:
		return FailureRateLimit
	case e.code == http.StatusPaymentRequired:
		return FailureQuota
	case e.code == http.StatusRequestEntityTooLarge || e.code == 413:
		return FailureContextWindowExceeded
	case e.code == http.StatusRequestTimeout || e.code == 408:
		return FailureTimeout
	case e.code >= 500 && e.code <= 599:
		return FailureServer
	case e.code == http.StatusBadRequest || e.code == http.StatusUnprocessableEntity:
		if IsContextOverflow(e) {
			return FailureContextWindowExceeded
		}
		return FailureInvalidRequest
	default:
		return FailureTransport
	}
}

func (e *httpStatusError) AsProviderFailure() *ProviderFailure {
	return &ProviderFailure{
		Code:       e.FailureCode(),
		Message:    e.Error(),
		Status:     e.code,
		RetryAfter: e.retryAfter,
		Err:        e,
	}
}

func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(header); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if parsedTime, err := http.ParseTime(header); err == nil {
		duration := time.Until(parsedTime)
		if duration > 0 {
			return duration
		}
	}
	return 0
}

func apiURL(endpoint, path string) string {
	return strings.TrimRight(endpoint, "/") + "/" + strings.TrimLeft(path, "/")
}

func jsonRequest(ctx context.Context, method, endpoint, apiKey string, headers http.Header, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return req, nil
}

func doJSON(ctx context.Context, client *http.Client, method, endpoint, apiKey string, headers http.Header, body any, out any) error {
	start := time.Now()
	logging.Info("HTTP request starting: method=%s endpoint=%s", method, endpoint)
	if logging.IsEnabled() && body != nil {
		if data, err := json.Marshal(body); err == nil {
			logging.Debug("HTTP wire request payload (endpoint=%s): %s", endpoint, string(data))
		}
	}
	req, err := jsonRequest(ctx, method, endpoint, apiKey, headers, body)
	if err != nil {
		logging.Error("HTTP request build error: %v", err)
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		logging.Error("HTTP request transport error (duration=%v): %v", time.Since(start), err)
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &ProviderFailure{Code: FailureAborted, Message: "request canceled by caller", Err: ctx.Err()}
		}
		return &ProviderFailure{Code: FailureTransport, Message: err.Error(), Err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		logging.Error("HTTP response read error: %v", err)
		return &ProviderFailure{Code: FailureTransport, Message: err.Error(), Err: err}
	}
	duration := time.Since(start)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		logging.Warn("HTTP request failed (status=%s duration=%v): %s", resp.Status, duration, string(data))
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		statusErr := &httpStatusError{code: resp.StatusCode, status: resp.Status, body: strings.TrimSpace(string(data)), retryAfter: retryAfter}
		return statusErr.AsProviderFailure()
	}
	logging.Info("HTTP request succeeded (status=%d duration=%v bytes=%d)", resp.StatusCode, duration, len(data))
	if logging.IsEnabled() {
		logging.Debug("HTTP wire response payload (endpoint=%s status=%d duration=%v): %s", endpoint, resp.StatusCode, duration, string(data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode provider response: %w", err)
	}
	return nil
}

func doJSONStream(ctx context.Context, client *http.Client, endpoint, apiKey, accept string, body any) (io.ReadCloser, http.Header, error) {
	start := time.Now()
	logging.Info("HTTP streaming request starting: endpoint=%s", endpoint)
	if logging.IsEnabled() && body != nil {
		if data, err := json.Marshal(body); err == nil {
			logging.Debug("HTTP wire streaming request payload (endpoint=%s): %s", endpoint, string(data))
		}
	}
	headers := make(http.Header)
	headers.Set("Accept", accept)
	req, err := jsonRequest(ctx, http.MethodPost, endpoint, apiKey, headers, body)
	if err != nil {
		logging.Error("HTTP streaming request build error: %v", err)
		return nil, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		logging.Error("HTTP streaming request transport error (duration=%v): %v", time.Since(start), err)
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, nil, &ProviderFailure{Code: FailureAborted, Message: "streaming request canceled", Err: ctx.Err()}
		}
		return nil, nil, &ProviderFailure{Code: FailureTransport, Message: err.Error(), Err: err}
	}
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		logging.Info("HTTP streaming connected (status=%d duration=%v)", resp.StatusCode, time.Since(start))
		return resp.Body, resp.Header, nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, nil, &ProviderFailure{Code: FailureTransport, Message: err.Error(), Err: err}
	}
	logging.Warn("HTTP streaming request failed (status=%s): %s", resp.Status, string(data))
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
	statusErr := &httpStatusError{code: resp.StatusCode, status: resp.Status, body: strings.TrimSpace(string(data)), retryAfter: retryAfter}
	return nil, nil, statusErr.AsProviderFailure()
}
