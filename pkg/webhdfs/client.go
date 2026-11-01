package webhdfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type Client struct {
	baseURL     string
	user        string
	http        *http.Client
	dataNodeURL *url.URL
}

type FileStatus struct {
	PathSuffix       string
	Type             string
	Length           int64
	ModificationTime int64
	Permission       string
}

type remoteError struct {
	RemoteException struct {
		Exception string
		Message   string
	}
}

func New(server string, port int, user string) (*Client, error) {
	if strings.TrimSpace(server) == "" || strings.ContainsAny(server, "/?#\\") {
		return nil, errors.New("invalid server name")
	}
	if port < 1 || port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}
	if strings.TrimSpace(user) == "" {
		return nil, errors.New("user name must not be empty")
	}
	return NewWithBaseURL(fmt.Sprintf("http://%s:%d", server, port), user, nil)
}

func NewWithBaseURL(baseURL, user string, client *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("invalid WebHDFS base URL")
	}
	if strings.TrimSpace(user) == "" {
		return nil, errors.New("user name must not be empty")
	}
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext
		transport.ResponseHeaderTimeout = 30 * time.Second
		client = &http.Client{Transport: transport}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), user: user, http: client}, nil
}

// NewWithEndpoints supports a single DataNode behind a Docker port mapping.
// An empty dataNodeURL preserves the addresses advertised by HDFS.
func NewWithEndpoints(baseURL, dataNodeURL, user string) (*Client, error) {
	c, err := NewWithBaseURL(baseURL, user, nil)
	if err != nil {
		return nil, err
	}
	if dataNodeURL == "" {
		return c, nil
	}
	u, err := url.Parse(dataNodeURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, errors.New("invalid DataNode URL; expected http(s)://host:port")
	}
	c.dataNodeURL = u
	c.http.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many WebHDFS redirects")
		}
		c.rewriteDataNode(req.URL)
		req.Host = req.URL.Host
		return nil
	}
	return c, nil
}

func (c *Client) rewriteDataNode(u *url.URL) {
	if c.dataNodeURL != nil {
		u.Scheme = c.dataNodeURL.Scheme
		u.Host = c.dataNodeURL.Host
	}
}

func (c *Client) Mkdir(hdfsPath string) error {
	resp, err := c.request(http.MethodPut, hdfsPath, "MKDIRS", nil, nil, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result struct {
		Boolean bool
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode MKDIRS response: %w", err)
	}
	if !result.Boolean {
		return errors.New("HDFS did not create the directory")
	}
	return nil
}

func (c *Client) Create(hdfsPath string, src io.Reader) error {
	return c.redirectedUpload(http.MethodPut, hdfsPath, "CREATE", url.Values{"overwrite": {"true"}}, src)
}

func (c *Client) Append(hdfsPath string, src io.Reader) error {
	return c.redirectedUpload(http.MethodPost, hdfsPath, "APPEND", nil, src)
}

// OpenReader streams a file from HDFS. The caller must close the returned body.
func (c *Client) OpenReader(hdfsPath string) (io.ReadCloser, error) {
	resp, err := c.request(http.MethodGet, hdfsPath, "OPEN", nil, nil, true)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) Open(hdfsPath string, dst io.Writer) error {
	body, err := c.OpenReader(hdfsPath)
	if err != nil {
		return err
	}
	defer body.Close()
	if _, err := io.Copy(dst, body); err != nil {
		return fmt.Errorf("download %s: %w", hdfsPath, err)
	}
	return nil
}

func (c *Client) Delete(hdfsPath string) error {
	resp, err := c.request(http.MethodDelete, hdfsPath, "DELETE", url.Values{"recursive": {"false"}}, nil, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result struct {
		Boolean bool
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode DELETE response: %w", err)
	}
	if !result.Boolean {
		return errors.New("HDFS did not delete the file")
	}
	return nil
}

func (c *Client) List(hdfsPath string) ([]FileStatus, error) {
	resp, err := c.request(http.MethodGet, hdfsPath, "LISTSTATUS", nil, nil, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result struct {
		FileStatuses struct {
			FileStatus []FileStatus
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode LISTSTATUS response: %w", err)
	}
	return result.FileStatuses.FileStatus, nil
}

func (c *Client) Status(hdfsPath string) (FileStatus, error) {
	resp, err := c.request(http.MethodGet, hdfsPath, "GETFILESTATUS", nil, nil, true)
	if err != nil {
		return FileStatus{}, err
	}
	defer resp.Body.Close()
	var result struct {
		FileStatus FileStatus
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return FileStatus{}, fmt.Errorf("decode GETFILESTATUS response: %w", err)
	}
	return result.FileStatus, nil
}

func (c *Client) redirectedUpload(method, hdfsPath, op string, query url.Values, src io.Reader) error {
	noRedirect := *c.http
	noRedirect.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	initialClient := *c
	initialClient.http = &noRedirect
	resp, err := initialClient.request(method, hdfsPath, op, query, nil, false)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusTemporaryRedirect {
		err := responseError(op, resp)
		resp.Body.Close()
		return err
	}
	location := resp.Header.Get("Location")
	resp.Body.Close()
	if location == "" {
		return fmt.Errorf("%s: redirect has no Location header", op)
	}

	req, err := http.NewRequest(method, location, src)
	if err != nil {
		return fmt.Errorf("build data-node request: %w", err)
	}
	c.rewriteDataNode(req.URL)
	req.Host = req.URL.Host
	dataResp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s data upload: %w", op, err)
	}
	defer dataResp.Body.Close()
	if dataResp.StatusCode < 200 || dataResp.StatusCode >= 300 {
		return responseError(op, dataResp)
	}
	return nil
}

func (c *Client) request(method, hdfsPath, op string, query url.Values, body io.Reader, require2xx bool) (*http.Response, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = "/webhdfs/v1" + normalizedPath(hdfsPath)
	q := u.Query()
	q.Set("op", op)
	q.Set("user.name", c.user)
	for key, values := range query {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", op, hdfsPath, err)
	}
	if require2xx && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		err := responseError(op, resp)
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

func responseError(op string, resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var parsed remoteError
	if json.Unmarshal(data, &parsed) == nil && parsed.RemoteException.Message != "" {
		return fmt.Errorf("%s: %s: %s", op, parsed.RemoteException.Exception, parsed.RemoteException.Message)
	}
	return fmt.Errorf("%s: HTTP %d: %s", op, resp.StatusCode, strings.TrimSpace(string(data)))
}

func normalizedPath(value string) string {
	clean := path.Clean("/" + value)
	return clean
}
