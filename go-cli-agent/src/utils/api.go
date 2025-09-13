package utils

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "net/http"
)

// APIClient is a struct that holds the base URL and any necessary headers for the API.
type APIClient struct {
    BaseURL string
    Headers map[string]string
}

// NewAPIClient initializes a new APIClient with the given base URL.
func NewAPIClient(baseURL string) *APIClient {
    return &APIClient{
        BaseURL: baseURL,
        Headers: make(map[string]string),
    }
}

// SetHeader allows setting a header for the API client.
func (client *APIClient) SetHeader(key, value string) {
    client.Headers[key] = value
}

// Post sends a POST request to the specified endpoint with the given payload.
func (client *APIClient) Post(endpoint string, payload interface{}) (*http.Response, error) {
    url := fmt.Sprintf("%s/%s", client.BaseURL, endpoint)
    jsonData, err := json.Marshal(payload)
    if err != nil {
        return nil, err
    }

    req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
    if err != nil {
        return nil, err
    }

    for key, value := range client.Headers {
        req.Header.Set(key, value)
    }
    req.Header.Set("Content-Type", "application/json")

    client := &http.Client{}
    return client.Do(req)
}

// Get sends a GET request to the specified endpoint.
func (client *APIClient) Get(endpoint string) (*http.Response, error) {
    url := fmt.Sprintf("%s/%s", client.BaseURL, endpoint)

    req, err := http.NewRequest("GET", url, nil)
    if err != nil {
        return nil, err
    }

    for key, value := range client.Headers {
        req.Header.Set(key, value)
    }

    client := &http.Client{}
    return client.Do(req)
}

// HandleResponse processes the HTTP response and returns the body as a byte slice.
func HandleResponse(resp *http.Response) ([]byte, error) {
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("error: received status code %d", resp.StatusCode)
    }
    return ioutil.ReadAll(resp.Body)
}