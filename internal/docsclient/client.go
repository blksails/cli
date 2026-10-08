// Package docsclient implements the authenticated HTTP client used by `bk docs`.
package docsclient

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
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New(baseURL, token string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("docs: 无效的服务地址 %q", baseURL)
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("docs: 缺少登录令牌，请运行 `bk auth login`")
	}
	return &Client{baseURL: baseURL, token: token, http: &http.Client{Timeout: 60 * time.Second}}, nil
}

type envelope struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
	Msg  string          `json:"msg"`
}

type File struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	ParentID string `json:"parent_id"`
}

type ListOptions struct {
	Cursor    string
	Limit     int
	Type      string
	Query     string
	Recursive bool
}

type ListResult struct {
	Items      []File `json:"items"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

type ContentResult struct {
	Format   string `json:"format"`
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

type SheetInfo struct {
	SheetID     string `json:"sheet_id"`
	Title       string `json:"title"`
	RowCount    int    `json:"row_count"`
	ColumnCount int    `json:"column_count"`
}

type ListSheetsResult struct {
	Sheets []SheetInfo `json:"sheets"`
}

type ReadRangeResult struct {
	Range  string     `json:"range"`
	Values [][]string `json:"values"`
}

type WriteRangeResult struct {
	UpdatedRows  int `json:"updated_rows"`
	UpdatedCells int `json:"updated_cells"`
}

type UploadResult struct {
	FileID string `json:"file_id"`
	URL    string `json:"url"`
}

type AuthStatus struct {
	Authenticated bool      `json:"authenticated"`
	OpenID        string    `json:"open_id"`
	ExpiresAt     time.Time `json:"expires_at"`
}

func (c *Client) List(ctx context.Context, folderID string, opts ListOptions) (*ListResult, error) {
	q := url.Values{}
	if folderID != "" {
		q.Set("folder_id", folderID)
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Type != "" {
		q.Set("type", opts.Type)
	}
	if opts.Query != "" {
		q.Set("q", opts.Query)
	}
	if opts.Recursive {
		q.Set("recursive", "true")
	}
	var out ListResult
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/files?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Content(ctx context.Context, fileID, format string) (*ContentResult, error) {
	p := "/api/v1/files/" + url.PathEscape(fileID) + "/content"
	if format != "" {
		p += "?format=" + url.QueryEscape(format)
	}
	var out ContentResult
	if err := c.doJSON(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Append(ctx context.Context, fileID, text string) error {
	p := "/api/v1/files/" + url.PathEscape(fileID) + "/content"
	return c.doJSON(ctx, http.MethodPost, p, map[string]string{"text": text}, nil)
}

func (c *Client) ListSheets(ctx context.Context, fileID string) (*ListSheetsResult, error) {
	p := "/api/v1/files/" + url.PathEscape(fileID) + "/sheets"
	var out ListSheetsResult
	if err := c.doJSON(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ReadRange(ctx context.Context, fileID, sheetID, cellRange string) (*ReadRangeResult, error) {
	p := "/api/v1/files/" + url.PathEscape(fileID) + "/sheets/" + url.PathEscape(sheetID) +
		"/values?range=" + url.QueryEscape(cellRange)
	var out ReadRangeResult
	if err := c.doJSON(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) WriteRange(ctx context.Context, fileID, sheetID, cellRange string, values [][]string) (*WriteRangeResult, error) {
	p := "/api/v1/files/" + url.PathEscape(fileID) + "/sheets/" + url.PathEscape(sheetID) + "/values"
	var out WriteRangeResult
	if err := c.doJSON(ctx, http.MethodPut, p, map[string]any{"range": cellRange, "values": values}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) AddSheet(ctx context.Context, fileID, title string, rows, cols int) (*SheetInfo, error) {
	p := "/api/v1/files/" + url.PathEscape(fileID) + "/sheets"
	var out SheetInfo
	if err := c.doJSON(ctx, http.MethodPost, p, map[string]any{"title": title, "row_count": rows, "column_count": cols}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteSheet(ctx context.Context, fileID, sheetID string) error {
	p := "/api/v1/files/" + url.PathEscape(fileID) + "/sheets/" + url.PathEscape(sheetID)
	return c.doJSON(ctx, http.MethodDelete, p, nil, nil)
}

func (c *Client) ClearRange(ctx context.Context, fileID, sheetID, cellRange string) error {
	p := "/api/v1/files/" + url.PathEscape(fileID) + "/sheets/" + url.PathEscape(sheetID) + "/values:clear"
	return c.doJSON(ctx, http.MethodPost, p, map[string]string{"range": cellRange}, nil)
}

func (c *Client) DeleteDimension(ctx context.Context, fileID, sheetID, dimension string, start, end int) error {
	p := "/api/v1/files/" + url.PathEscape(fileID) + "/sheets/" + url.PathEscape(sheetID) + "/dimension:delete"
	body := map[string]any{"dimension": dimension, "start": start, "end": end}
	return c.doJSON(ctx, http.MethodPost, p, body, nil)
}

func (c *Client) Export(ctx context.Context, fileID, format string) (string, error) {
	p := "/api/v1/files/" + url.PathEscape(fileID) + "/export"
	var out struct {
		DownloadURL string `json:"download_url"`
	}
	if err := c.doJSON(ctx, http.MethodPost, p, map[string]string{"format": format}, &out); err != nil {
		return "", err
	}
	if out.DownloadURL == "" {
		return "", fmt.Errorf("docs: 服务未返回下载地址")
	}
	return out.DownloadURL, nil
}

func (c *Client) Download(ctx context.Context, downloadURL string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("docs: 下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("docs: 下载返回 HTTP %d", resp.StatusCode)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func (c *Client) Upload(ctx context.Context, filePath, folderID string) (*UploadResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		var writeErr error
		defer func() { _ = pw.CloseWithError(writeErr) }()
		if folderID != "" {
			if writeErr = mw.WriteField("folder_id", folderID); writeErr != nil {
				return
			}
		}
		var part io.Writer
		part, writeErr = mw.CreateFormFile("file", filepath.Base(filePath))
		if writeErr != nil {
			return
		}
		_, writeErr = io.Copy(part, f)
		if writeErr != nil {
			return
		}
		writeErr = mw.Close()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/files/upload", pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.token)
	data, status, err := c.execute(req)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, c.statusError(status, data)
	}
	var out UploadResult
	if err := decodeEnvelope(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateFolder(ctx context.Context, name, parentID string) (string, error) {
	var out struct {
		FolderID string `json:"folder_id"`
	}
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/folders", map[string]string{"name": name, "parent_id": parentID}, &out)
	return out.FolderID, err
}

func (c *Client) Status(ctx context.Context) (*AuthStatus, error) {
	data, status, err := c.doRaw(ctx, http.MethodGet, "/api/auth/status", nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, c.statusError(status, data)
	}
	var out AuthStatus
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("docs: 解析授权状态失败: %w", err)
	}
	return &out, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	data, status, err := c.doRaw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return c.statusError(status, data)
	}
	return decodeEnvelope(data, out)
}

func (c *Client) doRaw(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return c.execute(req)
}

func (c *Client) execute(req *http.Request) ([]byte, int, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("docs: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func (c *Client) statusError(status int, data []byte) error {
	if status == http.StatusUnauthorized {
		return fmt.Errorf("docs: 登录已失效，请运行 `bk auth login`")
	}
	msg := strings.TrimSpace(string(data))
	var env envelope
	if json.Unmarshal(data, &env) == nil && env.Msg != "" {
		msg = env.Msg
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return fmt.Errorf("docs: 服务返回 HTTP %d: %s", status, msg)
}

func decodeEnvelope(data []byte, out any) error {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("docs: 解析响应失败: %w", err)
	}
	if env.Code != 0 {
		return fmt.Errorf("docs: %s", env.Msg)
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("docs: 解析响应数据失败: %w", err)
		}
	}
	return nil
}
