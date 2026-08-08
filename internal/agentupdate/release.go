package agentupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const maxMetadataBytes = 512 * 1024

type asset struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type manifest struct {
	Version        string  `json:"version"`
	SourceRevision string  `json:"sourceRevision"`
	ProtocolMajor  int     `json:"protocolMajor"`
	Assets         []asset `json:"assets"`
}

type currentRelease struct {
	Version        string `json:"version"`
	SourceRevision string `json:"sourceRevision"`
	ProtocolMajor  int    `json:"protocolMajor"`
	ManifestURL    string `json:"manifestUrl"`
}

type releaseClient struct {
	origin *url.URL
	http   *http.Client
}

func newReleaseClient(endpoint string, client *http.Client) (*releaseClient, error) {
	origin, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || origin.Host == "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return nil, errors.New("configured endpoint is invalid")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copy := *client
	priorRedirect := copy.CheckRedirect
	copy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if !sameOrigin(origin, request.URL) {
			return errors.New("agent release redirect crossed the configured origin")
		}
		if priorRedirect != nil {
			return priorRedirect(request, via)
		}
		if len(via) >= 10 {
			return errors.New("too many agent release redirects")
		}
		return nil
	}
	return &releaseClient{origin: origin, http: &copy}, nil
}

func sameOrigin(first, second *url.URL) bool {
	return strings.EqualFold(first.Scheme, second.Scheme) && strings.EqualFold(first.Host, second.Host)
}

func (client *releaseClient) resolve(reference string) (*url.URL, error) {
	parsed, err := url.Parse(reference)
	if err != nil {
		return nil, err
	}
	resolved := client.origin.ResolveReference(parsed)
	if !sameOrigin(client.origin, resolved) {
		return nil, errors.New("agent release URL crossed the configured origin")
	}
	return resolved, nil
}

func decodeResponse[T any](response *http.Response, limit int64, destination *T) error {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("agent release endpoint returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return errors.New("agent release response exceeds the size limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode agent release response: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("agent release response contains trailing data")
	}
	return nil
}

func (client *releaseClient) current(ctx context.Context) (currentRelease, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.origin.String()+"/api/agent/releases/current", nil)
	if err != nil {
		return currentRelease{}, err
	}
	response, err := client.http.Do(request)
	if err != nil {
		return currentRelease{}, err
	}
	var result currentRelease
	if err := decodeResponse(response, maxMetadataBytes, &result); err != nil {
		return currentRelease{}, err
	}
	if _, err := parseVersion(result.Version); err != nil || result.ProtocolMajor != 1 || result.ManifestURL == "" {
		return currentRelease{}, errors.New("current agent release descriptor is invalid")
	}
	if _, err := client.resolve(result.ManifestURL); err != nil {
		return currentRelease{}, err
	}
	return result, nil
}

func (client *releaseClient) manifest(ctx context.Context, version, manifestURL string) (manifest, error) {
	if _, err := parseVersion(version); err != nil {
		return manifest{}, err
	}
	if manifestURL == "" {
		manifestURL = "/api/agent/releases/" + url.PathEscape(version) + "/manifest.json"
	}
	resolved, err := client.resolve(manifestURL)
	if err != nil {
		return manifest{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resolved.String(), nil)
	if err != nil {
		return manifest{}, err
	}
	response, err := client.http.Do(request)
	if err != nil {
		return manifest{}, err
	}
	var result manifest
	if err := decodeResponse(response, maxMetadataBytes, &result); err != nil {
		return manifest{}, err
	}
	if result.Version != version || result.ProtocolMajor != 1 || len(result.Assets) < 1 || len(result.Assets) > 64 {
		return manifest{}, errors.New("agent release manifest is incompatible")
	}
	seen := map[string]struct{}{}
	for _, item := range result.Assets {
		clean := path.Clean(item.Path)
		if item.Path == "" || clean != item.Path || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || item.Bytes < 1 || item.Bytes > 128<<20 {
			return manifest{}, errors.New("agent release manifest contains an invalid asset")
		}
		decoded, err := hex.DecodeString(item.SHA256)
		if err != nil || len(decoded) != sha256.Size {
			return manifest{}, errors.New("agent release manifest contains an invalid digest")
		}
		if _, exists := seen[item.Path]; exists {
			return manifest{}, errors.New("agent release manifest contains duplicate assets")
		}
		seen[item.Path] = struct{}{}
	}
	return result, nil
}

func (client *releaseClient) download(ctx context.Context, version string, item asset) ([]byte, error) {
	reference := "/api/agent/releases/" + url.PathEscape(version) + "/" + item.Path
	resolved, err := client.resolve(reference)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resolved.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s returned HTTP %d", item.Path, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, item.Bytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != item.Bytes {
		return nil, fmt.Errorf("downloaded %s size does not match its manifest", item.Path)
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != item.SHA256 {
		return nil, fmt.Errorf("downloaded %s failed SHA-256 verification", item.Path)
	}
	return body, nil
}
