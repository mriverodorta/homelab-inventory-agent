package transport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

const (
	maxResponseBytes = 1024 * 1024
	signaturePrefix  = "homelab-inventory-agent-v1"
)

type HTTPError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("agent endpoint returned HTTP %d: %s", e.StatusCode, e.Message)
}

type ActivationResponse struct {
	DeviceID      uint64 `json:"deviceId"`
	ProtocolMajor int    `json:"protocolMajor"`
	ContractURL   string `json:"contractUrl"`
	HeartbeatURL  string `json:"heartbeatUrl"`
}

type HeartbeatResponse struct {
	OK               bool                       `json:"ok"`
	ReceivedAt       string                     `json:"receivedAt"`
	Sequence         uint64                     `json:"sequence"`
	MonitoringConfig *protocol.MonitoringConfig `json:"monitoringConfig,omitempty"`
	Telemetry        *TelemetryAcknowledgement  `json:"telemetry,omitempty"`
}

type TelemetryAcknowledgement struct {
	Duplicate           bool              `json:"duplicate"`
	AcceptedRevisions   map[string]uint64 `json:"acceptedRevisions"`
	Reconcile           []string          `json:"reconcile"`
	RequestCapabilities bool              `json:"requestCapabilities"`
}

type Client struct {
	endpoint   *url.URL
	httpClient *http.Client
	now        func() time.Time
}

func New(endpoint string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("agent endpoint is invalid")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{endpoint: parsed, httpClient: httpClient, now: time.Now}, nil
}

func (c *Client) route(path string) string {
	return c.endpoint.String() + path
}

func readResponse(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, errors.New("agent endpoint response exceeds the size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		var value struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if json.Unmarshal(body, &value) == nil && value.Message != "" {
			message = value.Message
		}
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return nil, &HTTPError{StatusCode: response.StatusCode, Code: value.Code, Message: message}
	}
	return body, nil
}

func decodeStrict(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("response contains trailing data")
	}
	return nil
}

func decodeExtensible(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("response contains trailing data")
	}
	return nil
}

func (c *Client) FetchContract(ctx context.Context, etag string) (protocol.Contract, string, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.route("/api/agent/contracts/current"), nil)
	if err != nil {
		return protocol.Contract{}, "", false, err
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	digest, err := protocol.BundleDigest()
	if err != nil {
		return protocol.Contract{}, "", false, err
	}
	request.Header.Set("X-Homelab-Agent-Schema-Digest", digest)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return protocol.Contract{}, "", false, err
	}
	if response.StatusCode == http.StatusNotModified {
		_ = response.Body.Close()
		return protocol.Contract{}, etag, true, nil
	}
	body, err := readResponse(response)
	if err != nil {
		return protocol.Contract{}, "", false, err
	}
	var contract protocol.Contract
	if err := decodeStrict(body, &contract); err != nil {
		return protocol.Contract{}, "", false, fmt.Errorf("decode agent contract: %w", err)
	}
	if err := protocol.ValidateContract(contract); err != nil {
		return protocol.Contract{}, "", false, err
	}
	compatible, err := protocol.IsCompatibleBundleDigest(contract.SchemaBundleDigest)
	if err != nil {
		return protocol.Contract{}, "", false, err
	}
	if !compatible {
		return protocol.Contract{}, "", false, errors.New("agent contract schema bundle is incompatible with this binary")
	}
	return contract, response.Header.Get("ETag"), false, nil
}

func (c *Client) Activate(ctx context.Context, host protocol.HostRef, token string, activation protocol.Activation) (ActivationResponse, error) {
	if err := protocol.ValidateHostRef(host); err != nil {
		return ActivationResponse{}, err
	}
	body, err := json.Marshal(activation)
	if err != nil {
		return ActivationResponse{}, err
	}
	path := fmt.Sprintf("/api/agent/hosts/%s/%d/activate", host.Type, host.ID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.route(path), bytes.NewReader(body))
	if err != nil {
		return ActivationResponse{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return ActivationResponse{}, err
	}
	responseBody, err := readResponse(response)
	if err != nil {
		return ActivationResponse{}, err
	}
	var result ActivationResponse
	if err := decodeStrict(responseBody, &result); err != nil {
		return ActivationResponse{}, fmt.Errorf("decode activation response: %w", err)
	}
	if result.DeviceID == 0 || result.DeviceID > 1<<53-1 || result.ProtocolMajor != protocol.CurrentMajor {
		return ActivationResponse{}, errors.New("activation response is invalid")
	}
	return result, nil
}

func CanonicalRequest(method, path, timestamp string, sequence uint64, bodyDigest string) []byte {
	return []byte(strings.Join([]string{
		signaturePrefix,
		strings.ToUpper(method),
		path,
		timestamp,
		strconv.FormatUint(sequence, 10),
		bodyDigest,
	}, "\n"))
}

func (c *Client) sendSigned(ctx context.Context, path, contentType, contentEncoding string, deviceID uint64, privateKey ed25519.PrivateKey, sequence uint64, body []byte) ([]byte, error) {
	if deviceID == 0 || sequence == 0 || len(privateKey) != ed25519.PrivateKeySize || len(body) == 0 {
		return nil, errors.New("signed request input is invalid")
	}
	timestamp := c.now().UTC().Format(time.RFC3339Nano)
	digestBytes := sha256.Sum256(body)
	digest := hex.EncodeToString(digestBytes[:])
	signature := ed25519.Sign(privateKey, CanonicalRequest(http.MethodPost, path, timestamp, sequence, digest))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.route(path), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", contentType)
	if contentEncoding != "" {
		request.Header.Set("Content-Encoding", contentEncoding)
	}
	request.Header.Set("X-Homelab-Agent-Id", strconv.FormatUint(deviceID, 10))
	request.Header.Set("X-Homelab-Agent-Timestamp", timestamp)
	request.Header.Set("X-Homelab-Agent-Sequence", strconv.FormatUint(sequence, 10))
	request.Header.Set("X-Homelab-Agent-Content-Sha256", digest)
	request.Header.Set("X-Homelab-Agent-Signature", base64.StdEncoding.EncodeToString(signature))
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	return readResponse(response)
}

func (c *Client) SendHeartbeat(ctx context.Context, host protocol.HostRef, deviceID uint64, privateKey ed25519.PrivateKey, sequence uint64, compressedBody []byte) (HeartbeatResponse, error) {
	if err := protocol.ValidateHostRef(host); err != nil {
		return HeartbeatResponse{}, err
	}
	path := fmt.Sprintf("/api/agent/hosts/%s/%d/heartbeats", host.Type, host.ID)
	body, err := c.sendSigned(ctx, path, "application/json", "gzip", deviceID, privateKey, sequence, compressedBody)
	if err != nil {
		return HeartbeatResponse{}, err
	}
	var result HeartbeatResponse
	if err := decodeExtensible(body, &result); err != nil {
		return HeartbeatResponse{}, fmt.Errorf("decode heartbeat response: %w", err)
	}
	if !result.OK || result.Sequence != sequence {
		return HeartbeatResponse{}, errors.New("heartbeat response is invalid")
	}
	if result.MonitoringConfig != nil {
		if err := protocol.ValidateMonitoringConfig(*result.MonitoringConfig); err != nil {
			return HeartbeatResponse{}, fmt.Errorf("heartbeat monitoring config is invalid: %w", err)
		}
	}
	return result, nil
}

func (c *Client) SendHardwareSnapshot(ctx context.Context, host protocol.HostRef, deviceID uint64, privateKey ed25519.PrivateKey, sequence uint64, body []byte) error {
	if err := protocol.ValidateHostRef(host); err != nil {
		return err
	}
	path := fmt.Sprintf("/api/agent/hosts/%s/%d/hardware-snapshots", host.Type, host.ID)
	_, err := c.sendSigned(ctx, path, "application/json", "", deviceID, privateKey, sequence, body)
	return err
}
