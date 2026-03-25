package bridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// VxeroClient is a lightweight HTTP client for the Vxero REST API.
type VxeroClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewVxeroClient creates a new API client.
func NewVxeroClient(baseURL, token string) *VxeroClient {
	return &VxeroClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *VxeroClient) do(method, path string, body any) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	url := c.BaseURL + "/api/v1" + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var apiErr struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &apiErr) == nil && apiErr.Message != "" {
			return nil, fmt.Errorf("API error: %s", apiErr.Message)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return data, nil
}

// --- Response types ---

// VxeroUser represents a Vxero user.
type VxeroUser struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// VxeroTeam represents a Vxero team.
type VxeroTeam struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// VxeroServer represents a server in Vxero.
type VxeroServer struct {
	ID            int     `json:"id"`
	Name          string  `json:"name"`
	IPAddress     *string `json:"ip_address"`
	Status        string  `json:"status"`
	InstallToken  *string `json:"install_token"`
}

// VxeroCreateServerResponse is the response from creating a server.
type VxeroCreateServerResponse struct {
	Server         VxeroServer `json:"server"`
	InstallCommand string      `json:"install_command"`
}

// VxeroCluster represents a cluster in Vxero.
type VxeroCluster struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// VxeroService represents a managed service in Vxero.
type VxeroService struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

// VxeroEnvironment represents an environment in Vxero.
type VxeroEnvironment struct {
	ID           int               `json:"id"`
	Name         string            `json:"name"`
	Slug         string            `json:"slug"`
	Domain       *string           `json:"domain"`
	EnvVariables map[string]string `json:"env_variables"`
}

// VxeroProject represents a project in Vxero.
type VxeroProject struct {
	ID           int                `json:"id"`
	Name         string             `json:"name"`
	Environments []VxeroEnvironment `json:"environments"`
}

// --- API methods ---

// Whoami verifies the token and returns user info.
func (c *VxeroClient) Whoami() (*VxeroUser, *VxeroTeam, error) {
	data, err := c.do(http.MethodGet, "/user", nil)
	if err != nil {
		return nil, nil, err
	}

	var resp struct {
		ID          int        `json:"id"`
		Name        string     `json:"name"`
		Email       string     `json:"email"`
		CurrentTeam *VxeroTeam `json:"current_team"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, fmt.Errorf("parse user: %w", err)
	}

	return &VxeroUser{ID: resp.ID, Name: resp.Name, Email: resp.Email}, resp.CurrentTeam, nil
}

// CreateServer registers a new server in Vxero.
func (c *VxeroClient) CreateServer(name, ip string, sshPort int) (*VxeroCreateServerResponse, error) {
	payload := map[string]any{
		"name":       name,
		"provider":   "custom",
		"ip_address": ip,
		"ssh_port":   sshPort,
	}

	data, err := c.do(http.MethodPost, "/servers", payload)
	if err != nil {
		return nil, err
	}

	var resp VxeroCreateServerResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse server response: %w", err)
	}

	return &resp, nil
}

// ListClusters returns available clusters.
func (c *VxeroClient) ListClusters() ([]VxeroCluster, error) {
	data, err := c.do(http.MethodGet, "/clusters", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data []VxeroCluster `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse clusters: %w", err)
	}

	return resp.Data, nil
}

// CreateService creates a managed database service on a cluster.
func (c *VxeroClient) CreateService(clusterID int, name, serviceType string) (*VxeroService, error) {
	payload := map[string]any{
		"name": name,
		"type": serviceType,
	}

	data, err := c.do(http.MethodPost, fmt.Sprintf("/clusters/%d/services", clusterID), payload)
	if err != nil {
		return nil, err
	}

	var svc VxeroService
	if err := json.Unmarshal(data, &svc); err != nil {
		return nil, fmt.Errorf("parse service: %w", err)
	}

	return &svc, nil
}

// GetServiceCredentials returns connection details for a managed service.
func (c *VxeroClient) GetServiceCredentials(clusterID, serviceID int) (map[string]string, error) {
	data, err := c.do(http.MethodGet, fmt.Sprintf("/clusters/%d/services/%d/credentials", clusterID, serviceID), nil)
	if err != nil {
		return nil, err
	}

	var creds map[string]string
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	return creds, nil
}

// UpdateEnvironment updates environment configuration.
func (c *VxeroClient) UpdateEnvironment(envID int, payload map[string]any) error {
	_, err := c.do(http.MethodPut, fmt.Sprintf("/environments/%d", envID), payload)
	return err
}
