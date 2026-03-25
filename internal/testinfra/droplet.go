package testinfra

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DOClient is a lightweight DigitalOcean API v2 client.
type DOClient struct {
	token      string
	httpClient *http.Client
}

// NewDOClient creates a new DigitalOcean API client.
func NewDOClient(token string) *DOClient {
	return &DOClient{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type doDropletRequest struct {
	Name    string   `json:"name"`
	Region  string   `json:"region"`
	Size    string   `json:"size"`
	Image   string   `json:"image"`
	SSHKeys []int    `json:"ssh_keys"`
	Tags    []string `json:"tags"`
}

type doDropletResponse struct {
	Droplet struct {
		ID       int    `json:"id"`
		Status   string `json:"status"`
		Networks struct {
			V4 []struct {
				IPAddress string `json:"ip_address"`
				Type      string `json:"type"`
			} `json:"v4"`
		} `json:"networks"`
	} `json:"droplet"`
}

type doSSHKeyRequest struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

type doSSHKeyResponse struct {
	SSHKey struct {
		ID int `json:"id"`
	} `json:"ssh_key"`
}

func (c *DOClient) doRequest(method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, "https://api.digitalocean.com/v2"+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("DO API %s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// UploadSSHKey uploads a public key to DigitalOcean and returns the key ID.
func (c *DOClient) UploadSSHKey(name, publicKey string) (int, error) {
	data, err := c.doRequest("POST", "/account/keys", doSSHKeyRequest{
		Name:      name,
		PublicKey: publicKey,
	})
	if err != nil {
		return 0, fmt.Errorf("upload SSH key: %w", err)
	}

	var resp doSSHKeyResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	return resp.SSHKey.ID, nil
}

// DeleteSSHKey removes an SSH key from DigitalOcean.
func (c *DOClient) DeleteSSHKey(id int) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/account/keys/%d", id), nil)
	return err
}

// CreateDroplet creates a new droplet and returns its ID.
func (c *DOClient) CreateDroplet(name, region, size, image string, sshKeyIDs []int) (int, error) {
	data, err := c.doRequest("POST", "/droplets", doDropletRequest{
		Name:    name,
		Region:  region,
		Size:    size,
		Image:   image,
		SSHKeys: sshKeyIDs,
		Tags:    []string{"neo-integration-test"},
	})
	if err != nil {
		return 0, fmt.Errorf("create droplet: %w", err)
	}

	var resp doDropletResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	return resp.Droplet.ID, nil
}

// WaitForDroplet polls until the droplet is active and returns its public IP.
func (c *DOClient) WaitForDroplet(id int) (string, error) {
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		data, err := c.doRequest("GET", fmt.Sprintf("/droplets/%d", id), nil)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		var resp doDropletResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		if resp.Droplet.Status == "active" {
			for _, net := range resp.Droplet.Networks.V4 {
				if net.Type == "public" {
					return net.IPAddress, nil
				}
			}
		}

		time.Sleep(5 * time.Second)
	}
	return "", fmt.Errorf("droplet %d did not become active within 5 minutes", id)
}

// DestroyDroplet deletes a droplet.
func (c *DOClient) DestroyDroplet(id int) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/droplets/%d", id), nil)
	return err
}
