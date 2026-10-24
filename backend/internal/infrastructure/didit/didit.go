package didit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	appcompliance "korzadi-wallet/backend/internal/application/compliance"
)

const (
	defaultBaseURL = "https://verification.didit.me"

	// Didit workflow_id is configuration, not a secret.
	// Replace this with the workflow_id from Didit Console.
	WorkflowID = "5cfc2272-2fbf-494d-879e-b19aa7d4e95d"
)

type Config struct {
	APIKey        string
	WebhookSecret string
	WorkflowID    string
	BaseURL       string
	HTTPClient    *http.Client
}

type Client struct {
	apiKey        string
	webhookSecret string
	workflowID    string
	baseURL       string
	httpClient    *http.Client
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("didit: missing API key")
	}
	if strings.TrimSpace(cfg.WebhookSecret) == "" {
		return nil, errors.New("didit: missing webhook secret")
	}
	if strings.TrimSpace(cfg.WorkflowID) == "" {
		return nil, errors.New("didit: missing workflow ID")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("didit: invalid base URL: %w", err)
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	return &Client{
		apiKey:        cfg.APIKey,
		webhookSecret: cfg.WebhookSecret,
		workflowID:    cfg.WorkflowID,
		baseURL:       baseURL,
		httpClient:    client,
	}, nil
}

func (c *Client) Name() string {
	return "didit"
}

type createSessionResponse struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
}

func (c *Client) CreateVerification(
	ctx context.Context,
	in appcompliance.CreateVerificationInput,
) (*appcompliance.CreateVerificationResult, error) {
	payload := map[string]interface{}{
		"workflow_id": c.workflowID,
		"vendor_data":  in.UserID.String(),
	}
	if strings.TrimSpace(in.Country) != "" {
		payload["expected_details"] = map[string]interface{}{
			"id_country":   strings.ToUpper(strings.TrimSpace(in.Country)),
			"nationality":  strings.ToUpper(strings.TrimSpace(in.Country)),
		}
		if strings.TrimSpace(in.ResidenceCountry) != "" {
			if ed, ok := payload["expected_details"].(map[string]interface{}); ok {
				ed["country"] = strings.ToUpper(strings.TrimSpace(in.ResidenceCountry))
			}
		}
	} else if strings.TrimSpace(in.ResidenceCountry) != "" {
		payload["expected_details"] = map[string]interface{}{
			"id_country":  strings.ToUpper(strings.TrimSpace(in.ResidenceCountry)),
			"nationality": strings.ToUpper(strings.TrimSpace(in.ResidenceCountry)),
			"country":     strings.ToUpper(strings.TrimSpace(in.ResidenceCountry)),
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("didit: encode session: %w", err)
	}

	var response createSessionResponse

	if err := c.requestJSON(
		ctx,
		http.MethodPost,
		"/v3/session/",
		body,
		&response,
	); err != nil {
		return nil, err
	}

	if strings.TrimSpace(response.SessionID) == "" {
		return nil, errors.New("didit: empty session_id")
	}

	if strings.TrimSpace(response.URL) == "" {
		return nil, errors.New("didit: empty verification URL")
	}

	return &appcompliance.CreateVerificationResult{
		ProviderReference: response.SessionID,
		RedirectURL:       response.URL,
	}, nil
}

type decisionResponse struct {
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`

	Decision struct {
		DeclineReasons []string `json:"decline_reasons"`

		IDVerifications []struct {
			DocumentType   string `json:"document_type"`
			DocumentNumber string `json:"document_number"`
			IssuingState   string `json:"issuing_state"`
		} `json:"id_verifications"`
	} `json:"decision"`
}

func (c *Client) GetVerificationStatus(
	ctx context.Context,
	providerReference string,
) (*appcompliance.ProviderStatus, error) {
	sessionID := strings.TrimSpace(providerReference)
	if sessionID == "" {
		return nil, errors.New("didit: empty session ID")
	}

	var response decisionResponse

	path := "/v3/session/" +
		url.PathEscape(sessionID) +
		"/decision/"

	if err := c.requestJSON(
		ctx,
		http.MethodGet,
		path,
		nil,
		&response,
	); err != nil {
		return nil, err
	}

	status, reason := mapStatus(response.Status)

	if reason == "" && len(response.Decision.DeclineReasons) > 0 {
		reason = normalizeReason(response.Decision.DeclineReasons[0])
	}

	var expiresAt *time.Time
	if response.ExpiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, response.ExpiresAt); err == nil {
			expiresAt = &parsed
		}
	}

	return &appcompliance.ProviderStatus{
		Status:     status,
		ReasonCode: reason,
		ExpiresAt:  expiresAt,
	}, nil
}

func (c *Client) CancelVerification(
	ctx context.Context,
	providerReference string,
) error {
	sessionID := strings.TrimSpace(providerReference)
	if sessionID == "" {
		return errors.New("didit: empty session ID")
	}

	path := "/v3/session/" +
		url.PathEscape(sessionID) +
		"/delete/"

	status, _, err := c.request(
		ctx,
		http.MethodDelete,
		path,
		nil,
	)

	if err != nil {
		return err
	}

	if status == http.StatusNoContent || status == http.StatusOK {
		return nil
	}

	// Didit documents 404 for an already deleted/unknown session.
	if status == http.StatusNotFound {
		return nil
	}

	return fmt.Errorf("didit: delete returned HTTP %d", status)
}

// VerifyWebhookSignature verifies Didit's V3 X-Signature-V2.
//
// Required:
//
//	X-Signature-V2
//	X-Timestamp
//
// Signature:
//
//	HMAC-SHA256(secret, canonical JSON)
//
// The freshness check is also performed here. The compliance service
// performs its own configured tolerance check after this method returns.
func (c *Client) VerifyWebhookSignature(
	headers map[string]string,
	body []byte,
	secret string,
) (time.Time, error) {
	if strings.TrimSpace(secret) == "" {
		secret = c.webhookSecret
	}

	if strings.TrimSpace(secret) == "" {
		return time.Time{}, appcompliance.ErrWebhookSignature
	}

	// Didit firma con X-Signature-V2 (JSON canónico). Si esa cabecera no
	// llega, se acepta X-Signature: HMAC-SHA256 del cuerpo crudo con el
	// mismo secreto. Ambas siguen exigiendo X-Timestamp fresco.
	signature := headerValue(headers, "X-Signature-V2")
	useRawBody := false
	if signature == "" {
		signature = headerValue(headers, "X-Signature")
		useRawBody = true
	}
	timestampHeader := headerValue(headers, "X-Timestamp")

	if signature == "" || timestampHeader == "" {
		return time.Time{}, appcompliance.ErrWebhookSignature
	}

	timestamp, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil || timestamp <= 0 {
		return time.Time{}, appcompliance.ErrWebhookSignature
	}

	signedAt := time.Unix(timestamp, 0)

	if delta := time.Since(signedAt); delta < -5*time.Minute ||
		delta > 5*time.Minute {
		return time.Time{}, appcompliance.ErrWebhookReplay
	}

	signedMessage := body

	if !useRawBody {
		var parsed any

		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()

		if err := decoder.Decode(&parsed); err != nil {
			return time.Time{}, appcompliance.ErrWebhookSignature
		}

		canonicalValue := shortenFloats(parsed)
		canonicalValue = sortKeys(canonicalValue)

		canonical, err := marshalCanonical(canonicalValue)
		if err != nil {
			return time.Time{}, appcompliance.ErrWebhookSignature
		}

		signedMessage = canonical
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(signedMessage)

	expected := hex.EncodeToString(mac.Sum(nil))

	got := strings.ToLower(strings.TrimSpace(signature))

	if len(got) != len(expected) ||
		!hmac.Equal([]byte(got), []byte(expected)) {
		return time.Time{}, appcompliance.ErrWebhookSignature
	}

	return signedAt, nil
}

type webhookPayload struct {
	EventID     string `json:"event_id"`
	WebhookType string `json:"webhook_type"`
	Timestamp   int64  `json:"timestamp"`
	CreatedAt   int64  `json:"created_at"`

	SessionID  string `json:"session_id"`
	Status     string `json:"status"`
	WorkflowID string `json:"workflow_id"`
	VendorData string `json:"vendor_data"`

	Decision struct {
		DeclineReasons []string `json:"decline_reasons"`

		IDVerifications []struct {
			DocumentType   string `json:"document_type"`
			DocumentNumber string `json:"document_number"`
			IssuingState   string `json:"issuing_state"`
		} `json:"id_verifications"`
	} `json:"decision"`
}

func (c *Client) HandleWebhook(
	ctx context.Context,
	body []byte,
) (*appcompliance.ProviderEvent, error) {
	_ = ctx

	var payload webhookPayload

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("didit: invalid webhook: %w", err)
	}

	if strings.TrimSpace(payload.EventID) == "" {
		return nil, errors.New("didit: webhook missing event_id")
	}

	if strings.TrimSpace(payload.SessionID) == "" {
		return nil, errors.New("didit: webhook missing session_id")
	}

	status, reason := mapStatus(payload.Status)

	if status == appcompliance.KYCNotStarted {
		return nil, fmt.Errorf(
			"didit: unsupported status %q",
			payload.Status,
		)
	}

	if reason == "" && len(payload.Decision.DeclineReasons) > 0 {
		reason = normalizeReason(payload.Decision.DeclineReasons[0])
	}

	var occurredAt time.Time

	if payload.Timestamp > 0 {
		occurredAt = time.Unix(payload.Timestamp, 0)
	} else if payload.CreatedAt > 0 {
		occurredAt = time.Unix(payload.CreatedAt, 0)
	}

	var documentType string
	var documentCountry string
	var documentHash string

	if len(payload.Decision.IDVerifications) > 0 {
		idv := payload.Decision.IDVerifications[0]

		documentType = strings.TrimSpace(idv.DocumentType)
		documentCountry = strings.TrimSpace(idv.IssuingState)

		if number := strings.TrimSpace(idv.DocumentNumber); number != "" {
			sum := sha256.Sum256([]byte(number))
			documentHash = hex.EncodeToString(sum[:])
		}
	}

	return &appcompliance.ProviderEvent{
		EventID:           payload.EventID,
		Type:              statusToEvent(status),
		ProviderReference: payload.SessionID,
		OccurredAt:        occurredAt,
		ReasonCode:        reason,
		DocumentType:      documentType,
		DocumentCountry:   documentCountry,
		DocumentHash:      documentHash,
	}, nil
}

func mapStatus(raw string) (appcompliance.KYCStatus, string) {
	switch raw {
	case "Approved":
		return appcompliance.KYCVerified, ""

	case "Declined":
		return appcompliance.KYCRejected, "DIDIT_DECLINED"

	case "In Review":
		return appcompliance.KYCInReview, ""

	case "In Progress",
		"Not Started",
		"Resubmitted":
		return appcompliance.KYCPending, ""

	case "Awaiting User":
		return appcompliance.KYCPending, ""

	case "Expired",
		"Kyc Expired",
		"Abandoned":
		return appcompliance.KYCExpired, "DIDIT_EXPIRED"

	default:
		return appcompliance.KYCNotStarted, ""
	}
}

func statusToEvent(status appcompliance.KYCStatus) string {
	switch status {
	case appcompliance.KYCPending:
		return appcompliance.EvtPending

	case appcompliance.KYCInReview:
		return appcompliance.EvtInReview

	case appcompliance.KYCVerified:
		return appcompliance.EvtApproved

	case appcompliance.KYCRejected:
		return appcompliance.EvtRejected

	case appcompliance.KYCExpired:
		return appcompliance.EvtExpired

	case appcompliance.KYCSuspended:
		return appcompliance.EvtSuspended

	default:
		return appcompliance.EvtPending
	}
}

func normalizeReason(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")

	if len(value) > 120 {
		value = value[:120]
	}

	return "DIDIT_" + value
}

func headerValue(headers map[string]string, wanted string) string {
	for key, value := range headers {
		if strings.EqualFold(key, wanted) {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func shortenFloats(value any) any {
	switch v := value.(type) {
	case []any:
		for i := range v {
			v[i] = shortenFloats(v[i])
		}
		return v

	case map[string]any:
		for key := range v {
			v[key] = shortenFloats(v[key])
		}
		return v

	case json.Number:
		f, err := strconv.ParseFloat(v.String(), 64)
		if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			if f == math.Trunc(f) {
				return strconv.FormatInt(int64(f), 10)
			}
		}
		return v

	default:
		return value
	}
}

func sortKeys(value any) any {
	switch v := value.(type) {
	case []any:
		for i := range v {
			v[i] = sortKeys(v[i])
		}
		return v

	case map[string]any:
		result := make(map[string]any, len(v))

		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}

		sort.Strings(keys)

		for _, key := range keys {
			result[key] = sortKeys(v[key])
		}

		return orderedObject{
			keys: keys,
			data: result,
		}

	default:
		return value
	}
}

type orderedObject struct {
	keys []string
	data map[string]any
}

func marshalCanonical(value any) ([]byte, error) {
	switch v := value.(type) {
	case orderedObject:
		var b bytes.Buffer
		b.WriteByte('{')

		for i, key := range v.keys {
			if i > 0 {
				b.WriteByte(',')
			}

			keyJSON, err := marshalString(key)
			if err != nil {
				return nil, err
			}

			b.Write(keyJSON)
			b.WriteByte(':')

			valueJSON, err := marshalCanonical(v.data[key])
			if err != nil {
				return nil, err
			}

			b.Write(valueJSON)
		}

		b.WriteByte('}')
		return b.Bytes(), nil

	case []any:
		var b bytes.Buffer
		b.WriteByte('[')

		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}

			itemJSON, err := marshalCanonical(item)
			if err != nil {
				return nil, err
			}

			b.Write(itemJSON)
		}

		b.WriteByte(']')
		return b.Bytes(), nil

	case string:
		return marshalString(v)

	case json.Number:
		return []byte(v.String()), nil

	default:
		var b bytes.Buffer

		encoder := json.NewEncoder(&b)
		encoder.SetEscapeHTML(false)

		if err := encoder.Encode(v); err != nil {
			return nil, err
		}

		result := bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
		return result, nil
	}
}

func marshalString(value string) ([]byte, error) {
	var b bytes.Buffer

	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(value); err != nil {
		return nil, err
	}

	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}

func (c *Client) requestJSON(
	ctx context.Context,
	method string,
	path string,
	body []byte,
	out any,
) error {
	status, responseBody, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}

	if status < 200 || status >= 300 {
		return fmt.Errorf("didit: HTTP %d", status)
	}

	if out == nil || len(responseBody) == 0 {
		return nil
	}

	if err := json.Unmarshal(responseBody, out); err != nil {
		return fmt.Errorf("didit: invalid response: %w", err)
	}

	return nil
}

func (c *Client) request(
	ctx context.Context,
	method string,
	path string,
	body []byte,
) (int, []byte, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		method,
		c.baseURL+path,
		bytes.NewReader(body),
	)
	if err != nil {
		return 0, nil, fmt.Errorf("didit: create request: %w", err)
	}

	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("didit: request failed: %w", err)
	}

	defer resp.Body.Close()

	responseBody, err := io.ReadAll(
		io.LimitReader(resp.Body, 2<<20),
	)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf(
			"didit: read response: %w",
			err,
		)
	}

	return resp.StatusCode, responseBody, nil
}

var _ appcompliance.KYCProvider = (*Client)(nil)
