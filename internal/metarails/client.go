package metarails

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Client struct {
	baseURL     string
	accessToken string
	httpClient  *http.Client
}

type DownloadEntry struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

type UploadResult struct {
	Total   int                `json:"total"`
	Success int                `json:"success"`
	Fail    int                `json:"fail"`
	Results []UploadItemResult `json:"results"`
}

type UploadItemResult struct {
	FileName   string `json:"file_name"`
	MaterialID int64  `json:"material_id,omitempty"`
	FileURL    string `json:"file_url,omitempty"`
	Error      string `json:"error,omitempty"`
}

func New(endpoint, accessToken string) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid webapp endpoint %q", endpoint)
	}
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("access token is required")
	}
	return &Client{baseURL: strings.TrimRight(u.String(), "/"), accessToken: accessToken, httpClient: http.DefaultClient}, nil
}

func (c *Client) Download(ctx context.Context, entries []DownloadEntry, w io.Writer) error {
	if len(entries) == 0 {
		return fmt.Errorf("at least one download entry is required")
	}
	body, err := json.Marshal(map[string]any{"entries": entries})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/features/materials/downloadZip", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download materials: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError("download materials", resp)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("write downloaded materials: %w", err)
	}
	return nil
}

func (c *Client) Upload(ctx context.Context, paths []string, materialType string, folderID int64, tags []string) (*UploadResult, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one upload file is required")
	}
	reader, writer := io.Pipe()
	mw := multipart.NewWriter(writer)
	writeErr := make(chan error, 1)
	go func() {
		defer close(writeErr)
		fail := func(err error) { _ = writer.CloseWithError(err); writeErr <- err }
		if err := mw.WriteField("material_type", materialType); err != nil {
			fail(err)
			return
		}
		if folderID > 0 {
			if err := mw.WriteField("folder_id", fmt.Sprint(folderID)); err != nil {
				fail(err)
				return
			}
		}
		if len(tags) > 0 {
			encoded, err := json.Marshal(tags)
			if err != nil {
				fail(err)
				return
			}
			if err := mw.WriteField("tags", string(encoded)); err != nil {
				fail(err)
				return
			}
		}
		for _, path := range paths {
			file, err := os.Open(path)
			if err != nil {
				fail(err)
				return
			}
			part, err := mw.CreateFormFile("files", filepath.Base(path))
			if err == nil {
				_, err = io.Copy(part, file)
			}
			closeErr := file.Close()
			if err != nil {
				fail(err)
				return
			}
			if closeErr != nil {
				fail(closeErr)
				return
			}
		}
		if err := mw.Close(); err != nil {
			fail(err)
			return
		}
		writeErr <- writer.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/materials/upload", reader)
	if err != nil {
		_ = reader.CloseWithError(err)
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		_ = reader.CloseWithError(err)
		<-writeErr
		return nil, fmt.Errorf("upload materials: %w", err)
	}
	// A server may reject authentication before consuming the multipart body.
	// Closing the pipe unblocks the producer in that case; after a normal upload
	// the transport has already consumed it and this is a no-op.
	_ = reader.Close()
	producerErr := <-writeErr
	defer resp.Body.Close()
	if producerErr != nil {
		return nil, fmt.Errorf("build upload request: %w", producerErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError("upload materials", resp)
	}
	var result UploadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode upload response: %w", err)
	}
	return &result, nil
}

func responseError(action string, resp *http.Response) error {
	const maxErrorBody = 64 << 10
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	message := strings.TrimSpace(string(b))
	var envelope struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(b, &envelope) == nil && envelope.Error != nil {
		switch value := envelope.Error.(type) {
		case string:
			message = value
		case map[string]any:
			if text, ok := value["message"].(string); ok {
				message = text
			}
		}
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("%s: HTTP %d: %s", action, resp.StatusCode, message)
}
