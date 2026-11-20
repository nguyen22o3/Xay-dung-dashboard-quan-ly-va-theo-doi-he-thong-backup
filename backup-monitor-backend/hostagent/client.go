// Package hostagent connects the unprivileged API to the trusted local host
// service. The socket and credential are root-equivalent capabilities: never
// publish this protocol over TCP or expose it as a browser API.
package hostagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const MaxRequest = 4 << 20
const MaxOutput = 32 << 20

type Request struct {
	Command string `json:"command"`
	Input   []byte `json:"input,omitempty"`
}
type Response struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}
type Client struct {
	socket, token string
	http          *http.Client
}

func NewClient(socket, token string) (*Client, error) {
	if !strings.HasPrefix(socket, "/") || len(token) < 43 || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("invalid local agent configuration")
	}
	transport := &http.Transport{MaxIdleConnsPerHost: 16, ResponseHeaderTimeout: 11 * time.Minute,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}}
	return &Client{socket: socket, token: token, http: &http.Client{Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) request(ctx context.Context, path, command string, input []byte) (*http.Response, error) {
	body, err := json.Marshal(Request{Command: command, Input: input})
	if err != nil || len(body) > MaxRequest {
		return nil, errors.New("local agent request too large")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://host-agent"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("Không kết nối được công cụ cục bộ trên máy chủ")
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, errors.New("Công cụ cục bộ từ chối yêu cầu; kiểm tra dịch vụ và quyền truy cập")
	}
	return res, nil
}
func (c *Client) Execute(ctx context.Context, command string, input []byte) (string, error) {
	res, err := c.request(ctx, "/execute", command, input)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 6*MaxOutput+1))
	if err != nil || len(data) > 6*MaxOutput {
		return "", errors.New("invalid local agent response")
	}
	var reply Response
	if json.Unmarshal(data, &reply) != nil {
		return "", errors.New("invalid local agent response")
	}
	if reply.Error != "" {
		return "", errors.New(reply.Error)
	}
	return reply.Output, nil
}
func (c *Client) Stream(ctx context.Context, command string, dst io.Writer) error {
	res, err := c.request(ctx, "/stream", command, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if _, err = io.Copy(dst, res.Body); err != nil {
		return err
	}
	if res.Trailer.Get("X-Agent-Result") != "ok" {
		return errors.New("Tải bản sao lưu không hoàn tất")
	}
	return nil
}
