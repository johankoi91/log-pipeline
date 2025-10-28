package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

func init() {
	// JS/CSS/Map/字体/图片/wasm 等常见前端资源
	_ = mime.AddExtensionType(".html", "text/html")
	_ = mime.AddExtensionType(".js", "text/javascript")
	_ = mime.AddExtensionType(".mjs", "text/javascript")
	_ = mime.AddExtensionType(".css", "text/css")
	_ = mime.AddExtensionType(".map", "application/json")
	_ = mime.AddExtensionType(".json", "application/json")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".wasm", "application/wasm")
	_ = mime.AddExtensionType(".ico", "image/x-icon")
	_ = mime.AddExtensionType(".woff", "font/woff")
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".ttf", "font/ttf")
}

/************** 配置 **************/

type Config struct {
	ES struct {
		Host      string `yaml:"host"`
		Username  string `yaml:"username"`
		Password  string `yaml:"password"`
		VerifyTLS bool   `yaml:"verify_tls"`
		Names     struct {
			DataStream    string `yaml:"data_stream"`
			ILMPolicy     string `yaml:"ilm_policy"`
			IndexTemplate string `yaml:"index_template"`
			Pipeline      string `yaml:"pipeline"`
		} `yaml:"names"`
		Files struct {
			ILM      string `yaml:"ilm"`
			Template string `yaml:"template"`
			Pipeline string `yaml:"pipeline"`
		} `yaml:"files"`
	} `yaml:"es"`
	Connect struct {
		Host     string `yaml:"host"`
		Username string `yaml:"username"`
		Password string `yaml:"password"`
		Names    struct {
			Sink string `yaml:"sink"`
		} `yaml:"names"`
		Files struct {
			Sink string `yaml:"sink"`
		} `yaml:"files"`
	} `yaml:"connect"`

	Frontend struct {
		AllowedOrigins []string `yaml:"allowed_origins"`
	} `yaml:"frontend"`
}

/************** 服务器对象 **************/

type Server struct {
	cfg    Config
	client *http.Client
	logger *log.Logger
}

/************** 启动参数（支持 ENV 覆盖） **************/

var (
	flagListen = flag.String("listen", ":8801", "HTTP listen address, e.g. :80")
	flagStatic = flag.String("static-dir", "./static", "Directory of built frontend (must contain index.html)")
)

func withEnv(v *string, envKey string) {
	if val := strings.TrimSpace(os.Getenv(envKey)); val != "" {
		*v = val
	}
}

/************** 工具函数 **************/

func mustReadYAML(path string, out any) {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	if err := yaml.Unmarshal(b, out); err != nil {
		panic(err)
	}
}

func newHTTPClient(skipVerify bool) *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify}, //nolint:gosec
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConnsPerHost:   8,
	}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

func (s *Server) withESAuth(req *http.Request) {
	if s.cfg.ES.Username != "" {
		req.SetBasicAuth(s.cfg.ES.Username, s.cfg.ES.Password)
	}
}
func (s *Server) withConnectAuth(req *http.Request) {
	if s.cfg.Connect.Username != "" {
		req.SetBasicAuth(s.cfg.Connect.Username, s.cfg.Connect.Password)
	}
}

func readJSONFile(path string) ([]byte, error) {
	p := filepath.Clean(path)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", p, err)
	}
	return b, nil
}

// 简单 bytes Reader（避免引额外依赖）
func bytesReader(b []byte) *reader { return &reader{b: b} }

type reader struct{ b []byte }

func (r *reader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonRaw(b []byte) map[string]any {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return map[string]any{"raw": string(b)}
	}
	return map[string]any{"data": v}
}

/************** 请求日志中间件 **************/

// 计算客户端 IP（兼容 X-Forwarded-For）
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > -1 {
		return host[:i]
	}
	return host
}

// 记录状态码与响应大小
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func requestLogger(l *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(sr, r)
		dur := time.Since(start)

		origin := r.Header.Get("Origin")
		clen := r.Header.Get("Content-Length")
		if clen == "" {
			clen = "0"
		}

		l.Printf(
			"http req method=%s path=%s query=%q origin=%q ip=%s status=%d bytes=%d dur_ms=%.3f req_bytes=%s ua=%q",
			r.Method, r.URL.Path, r.URL.RawQuery, origin, clientIP(r), sr.status, sr.bytes,
			float64(dur.Microseconds())/1000.0, clen, r.UserAgent(),
		)
	})
}

/************** CORS 中间件（多域白名单） **************/

func cors(allowed []string, next http.Handler) http.Handler {
	allowSet := map[string]struct{}{}
	for _, o := range allowed {
		allowSet[o] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 如需严格白名单，请恢复下方逻辑并去掉通配：
		// origin := r.Header.Get("Origin")
		// if origin != "" {
		// 	if _, ok := allowSet[origin]; ok {
		// 		w.Header().Set("Access-Control-Allow-Origin", origin)
		// 		// w.Header().Set("Access-Control-Allow-Credentials", "true")
		// 	}
		// }
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "600")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

/************** 下游调用日志 **************/

func (s *Server) logDownstream(kind, method, url, file string, status int, body []byte, err error) {
	const maxDump = 2048
	snippet := body
	if len(snippet) > maxDump {
		snippet = body[:maxDump]
	}
	if err != nil {
		s.logger.Printf("downstream kind=%s method=%s url=%s file=%s status=%d err=%v body=%q",
			kind, method, url, file, status, err, string(snippet))
		return
	}
	if status >= 400 {
		s.logger.Printf("downstream kind=%s method=%s url=%s file=%s status=%d body=%q",
			kind, method, url, file, status, string(snippet))
	} else {
		s.logger.Printf("downstream kind=%s method=%s url=%s file=%s status=%d",
			kind, method, url, file, status)
	}
}

/************** 通用 HTTP 方法（带日志） **************/

func (s *Server) doPUT(ctx context.Context, url string, body []byte, esOrConnect string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytesReader(body))
	if err != nil {
		s.logDownstream(esOrConnect+"|put", "PUT", url, "", 0, nil, err)
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if esOrConnect == "es" {
		s.withESAuth(req)
	} else {
		s.withConnectAuth(req)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.logDownstream(esOrConnect+"|put", "PUT", url, "", 0, nil, err)
		return nil, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	s.logDownstream(esOrConnect+"|put", "PUT", url, "", resp.StatusCode, respBody, nil)
	return resp, respBody, nil
}

func (s *Server) doPUTNoBody(ctx context.Context, url string, esOrConnect string) (*http.Response, []byte, error) {
	return s.doPUT(ctx, url, []byte{}, esOrConnect)
}

func (s *Server) doGET(ctx context.Context, url string, esOrConnect string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		s.logDownstream(esOrConnect+"|get", "GET", url, "", 0, nil, err)
		return nil, nil, err
	}
	if esOrConnect == "es" {
		s.withESAuth(req)
	} else {
		s.withConnectAuth(req)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.logDownstream(esOrConnect+"|get", "GET", url, "", 0, nil, err)
		return nil, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	s.logDownstream(esOrConnect+"|get", "GET", url, "", resp.StatusCode, respBody, nil)
	return resp, respBody, nil
}

func (s *Server) doPOST(ctx context.Context, url string, body []byte, esOrConnect string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytesReader(body))
	if err != nil {
		s.logDownstream(esOrConnect+"|post", "POST", url, "", 0, nil, err)
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if esOrConnect == "es" {
		s.withESAuth(req)
	} else {
		s.withConnectAuth(req)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.logDownstream(esOrConnect+"|post", "POST", url, "", 0, nil, err)
		return nil, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	s.logDownstream(esOrConnect+"|post", "POST", url, "", resp.StatusCode, respBody, nil)
	return resp, respBody, nil
}

func (s *Server) doDELETE(ctx context.Context, url string, esOrConnect string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		s.logDownstream(esOrConnect+"|delete", "DELETE", url, "", 0, nil, err)
		return nil, nil, err
	}
	if esOrConnect == "es" {
		s.withESAuth(req)
	} else {
		s.withConnectAuth(req)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.logDownstream(esOrConnect+"|delete", "DELETE", url, "", 0, nil, err)
		return nil, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	s.logDownstream(esOrConnect+"|delete", "DELETE", url, "", resp.StatusCode, respBody, nil)
	return resp, respBody, nil
}

/************** 业务处理：创建/更新 **************/

func (s *Server) handleClientConfig(w http.ResponseWriter, r *http.Request) {
	// 防缓存
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	var cfg Config
	mustReadYAML("config.yaml", &cfg)

	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleCreateDataStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	url := fmt.Sprintf("%s/_data_stream/%s", s.cfg.ES.Host, s.cfg.ES.Names.DataStream)
	s.logger.Printf("step=data-stream put url=%s", url)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, url, nil)
	s.withESAuth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		writeJSON(w, 500, map[string]any{"step": "data-stream", "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	writeJSON(w, resp.StatusCode, map[string]any{
		"step":   "data-stream",
		"status": resp.Status,
		"body":   string(body),
	})
}

func (s *Server) handlePutILM(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	file := s.cfg.ES.Files.ILM
	b, err := readJSONFile(file)
	if err != nil {
		s.logger.Printf("step=ilm read_file_err file=%s err=%v", file, err)
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	url := fmt.Sprintf("%s/_ilm/policy/%s", s.cfg.ES.Host, s.cfg.ES.Names.ILMPolicy)
	s.logger.Printf("step=ilm put url=%s file=%s size=%d", url, file, len(b))
	resp, respBody, err := s.doPUT(ctx, url, b, "es")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, map[string]any{"step": "ilm", "status": resp.Status, "body": string(respBody)})
}

func (s *Server) handlePutTemplate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	file := s.cfg.ES.Files.Template
	b, err := readJSONFile(file)
	if err != nil {
		s.logger.Printf("step=template read_file_err file=%s err=%v", file, err)
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	url := fmt.Sprintf("%s/_index_template/%s", s.cfg.ES.Host, s.cfg.ES.Names.IndexTemplate)
	s.logger.Printf("step=template put url=%s file=%s size=%d", url, file, len(b))
	resp, respBody, err := s.doPUT(ctx, url, b, "es")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, map[string]any{"step": "template", "status": resp.Status, "body": string(respBody)})
}

func (s *Server) handlePutPipeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	file := s.cfg.ES.Files.Pipeline
	b, err := readJSONFile(file)
	if err != nil {
		s.logger.Printf("step=pipeline read_file_err file=%s err=%v", file, err)
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	url := fmt.Sprintf("%s/_ingest/pipeline/%s", s.cfg.ES.Host, s.cfg.ES.Names.Pipeline)
	s.logger.Printf("step=pipeline put url=%s file=%s size=%d", url, file, len(b))
	resp, respBody, err := s.doPUT(ctx, url, b, "es")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, map[string]any{"step": "pipeline", "status": resp.Status, "body": string(respBody)})
}

func (s *Server) handleRegisterSink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	file := s.cfg.Connect.Files.Sink
	b, err := readJSONFile(file)
	if err != nil {
		s.logger.Printf("step=sink read_file_err file=%s err=%v", file, err)
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	url := fmt.Sprintf("%s/connectors", s.cfg.Connect.Host)
	s.logger.Printf("step=sink post url=%s file=%s size=%d", url, file, len(b))
	resp, respBody, err := s.doPOST(ctx, url, b, "connect")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, map[string]any{"step": "sink", "status": resp.Status, "body": string(respBody)})
}

type captureWriter struct {
	hdr        http.Header
	status     int
	statusText string
	body       string
	err        string
}

func (c *captureWriter) Header() http.Header { return c.hdr }
func (c *captureWriter) WriteHeader(statusCode int) {
	c.status = statusCode
	c.statusText = http.StatusText(statusCode)
}
func (c *captureWriter) Write(b []byte) (int, error) { c.body += string(b); return len(b), nil }

/************** 业务处理：验证查看 **************/

func (s *Server) handleVerifyILMExplain(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	url := fmt.Sprintf("%s/%s/_ilm/explain", s.cfg.ES.Host, s.cfg.ES.Names.DataStream)
	s.logger.Printf("verify=ilm-explain url=%s", url)
	resp, body, err := s.doGET(ctx, url, "es")
	if err != nil {
		writeJSON(w, 500, map[string]any{"step": "verify-ilm", "error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, jsonRaw(body))
}

func (s *Server) handleVerifyTemplate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	url := fmt.Sprintf("%s/_index_template/%s", s.cfg.ES.Host, s.cfg.ES.Names.IndexTemplate)
	s.logger.Printf("verify=index-template url=%s", url)
	resp, body, err := s.doGET(ctx, url, "es")
	if err != nil {
		writeJSON(w, 500, map[string]any{"step": "verify-template", "error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, jsonRaw(body))
}

func (s *Server) handleVerifyPipeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	url := fmt.Sprintf("%s/_ingest/pipeline/%s", s.cfg.ES.Host, s.cfg.ES.Names.Pipeline)
	s.logger.Printf("verify=pipeline url=%s", url)
	resp, body, err := s.doGET(ctx, url, "es")
	if err != nil {
		writeJSON(w, 500, map[string]any{"step": "verify-pipeline", "error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, jsonRaw(body))
}

func (s *Server) handleVerifySinkStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	url := fmt.Sprintf("%s/connectors/%s/status", s.cfg.Connect.Host, s.cfg.Connect.Names.Sink)
	s.logger.Printf("verify=sink-status url=%s", url)
	resp, body, err := s.doGET(ctx, url, "connect")
	if err != nil {
		writeJSON(w, 500, map[string]any{"step": "verify-sink-status", "error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, jsonRaw(body))
}

func (s *Server) handleQueryDataStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	url := fmt.Sprintf("%s/_data_stream/*?pretty", s.cfg.ES.Host)
	s.logger.Printf("_data_stream url=%s", url)
	resp, body, err := s.doGET(ctx, url, "es")
	if err != nil {
		writeJSON(w, 500, map[string]any{"step": "query _data_stream", "error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, jsonRaw(body))
}

/************** 业务处理：维护（Kafka Connect） **************/

func (s *Server) handleGetSinkConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	url := fmt.Sprintf("%s/connectors/%s/config", s.cfg.Connect.Host, s.cfg.Connect.Names.Sink)
	s.logger.Printf("connect action=get-config name=%s url=%s", s.cfg.Connect.Names.Sink, url)
	resp, body, err := s.doGET(ctx, url, "connect")
	if err != nil {
		writeJSON(w, 500, map[string]any{"step": "connect-config", "error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, jsonRaw(body))
}

func (s *Server) handlePauseSink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	url := fmt.Sprintf("%s/connectors/%s/pause", s.cfg.Connect.Host, s.cfg.Connect.Names.Sink)
	s.logger.Printf("connect action=pause name=%s url=%s", s.cfg.Connect.Names.Sink, url)
	resp, body, err := s.doPUTNoBody(ctx, url, "connect")
	if err != nil {
		writeJSON(w, 500, map[string]any{"step": "connect-pause", "error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, jsonRaw(body))
}

func (s *Server) handleResumeSink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	url := fmt.Sprintf("%s/connectors/%s/resume", s.cfg.Connect.Host, s.cfg.Connect.Names.Sink)
	s.logger.Printf("connect action=resume name=%s url=%s", s.cfg.Connect.Names.Sink, url)
	resp, body, err := s.doPUTNoBody(ctx, url, "connect")
	if err != nil {
		writeJSON(w, 500, map[string]any{"step": "connect-resume", "error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, jsonRaw(body))
}

func (s *Server) handleDeleteSink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	url := fmt.Sprintf("%s/connectors/%s", s.cfg.Connect.Host, s.cfg.Connect.Names.Sink)
	s.logger.Printf("connect action=delete name=%s url=%s", s.cfg.Connect.Names.Sink, url)
	resp, body, err := s.doDELETE(ctx, url, "connect")
	if err != nil {
		writeJSON(w, 500, map[string]any{"step": "connect-delete", "error": err.Error()})
		return
	}
	writeJSON(w, resp.StatusCode, jsonRaw(body))
}

/************** 静态文件 + SPA 回退 **************/

type spaHandler struct {
	staticDir    string
	indexFile    string
	adminHandler http.Handler
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1) /admin/* -> 交给 API
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		if h.adminHandler != nil {
			h.adminHandler.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}

	// 2) 静态文件或 SPA 回退
	// 根路径直接返回 index.html
	if r.URL.Path == "/" || r.URL.Path == "" {
		http.ServeFile(w, r, filepath.Join(h.staticDir, h.indexFile))
		return
	}

	clean := filepath.Clean(r.URL.Path)
	try := filepath.Join(h.staticDir, clean)

	if fi, err := os.Stat(try); err == nil && !fi.IsDir() {
		http.ServeFile(w, r, try)
		return
	}

	// 未命中文件 -> SPA 回退到 index.html
	http.ServeFile(w, r, filepath.Join(h.staticDir, h.indexFile))
}

/************** main **************/

func main() {
	flag.Parse()
	withEnv(flagListen, "LISTEN")
	withEnv(flagStatic, "STATIC_DIR")

	var cfg Config
	mustReadYAML("config.yaml", &cfg)

	s := &Server{
		cfg: cfg,
		// 注意：VerifyTLS=true 表示“校验证书”，我们创建 client 时需要传入“是否跳过校验”
		// 所以这里用 newHTTPClient(!cfg.ES.VerifyTLS)
		client: newHTTPClient(!cfg.ES.VerifyTLS),
		logger: log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds),
	}

	// --- 构建 /admin/* 的路由（沿用你现有的全部业务处理） ---
	adminMux := http.NewServeMux()

	adminMux.HandleFunc("GET /admin/client-config", s.handleClientConfig)

	// 创建/更新
	adminMux.HandleFunc("POST /admin/es/data-stream", s.handleCreateDataStream)
	adminMux.HandleFunc("POST /admin/es/ilm", s.handlePutILM)
	adminMux.HandleFunc("POST /admin/es/template", s.handlePutTemplate)
	adminMux.HandleFunc("POST /admin/es/pipeline", s.handlePutPipeline)
	adminMux.HandleFunc("POST /admin/connect/sink", s.handleRegisterSink)

	// 验证查看
	adminMux.HandleFunc("GET /admin/verify/ilm-explain", s.handleVerifyILMExplain)
	adminMux.HandleFunc("GET /admin/verify/template", s.handleVerifyTemplate)
	adminMux.HandleFunc("GET /admin/verify/pipeline", s.handleVerifyPipeline)
	adminMux.HandleFunc("GET /admin/query/data-streams", s.handleQueryDataStream)
	adminMux.HandleFunc("GET /admin/verify/sink-status", s.handleVerifySinkStatus)

	// 维护（Connect）
	adminMux.HandleFunc("GET /admin/connect/config", s.handleGetSinkConfig)
	adminMux.HandleFunc("PUT /admin/connect/pause", s.handlePauseSink)
	adminMux.HandleFunc("PUT /admin/connect/resume", s.handleResumeSink)
	adminMux.HandleFunc("DELETE /admin/connect/delete", s.handleDeleteSink)

	// 给 /admin/* 包上 CORS 和请求日志
	adminHandler := requestLogger(s.logger, cors(cfg.Frontend.AllowedOrigins, adminMux))

	// --- 顶层：静态 + SPA 回退 + /admin 代理 ---
	root := http.NewServeMux()
	root.Handle("/", &spaHandler{
		staticDir:    *flagStatic,
		indexFile:    "index.html",
		adminHandler: adminHandler,
	})

	// 额外：如果你的前端产物使用 /static 前缀，也可直出（非必需）
	if _, err := os.Stat(*flagStatic); err == nil {
		root.Handle("/static/", http.FileServer(http.Dir(*flagStatic)))
		root.Handle("/asset-manifest.json", http.FileServer(http.Dir(*flagStatic)))
		root.Handle("/favicon.ico", http.FileServer(http.Dir(*flagStatic)))
	}

	srv := &http.Server{
		Addr:              *flagListen,
		Handler:           requestLogger(s.logger, root), // 顶层也记一次日志（包含静态）
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// 校验静态目录（不存在也不退出，让 API 可用）
	if _, err := os.Stat(filepath.Join(*flagStatic, "index.html")); err != nil {
		s.logger.Printf("warning: index.html not found in static dir: %s (err=%v)", *flagStatic, err)
	}

	// 优雅关机
	idleConnsClosed := make(chan struct{})
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		sig := <-ch
		s.logger.Printf("signal=%s shutting down...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Printf("graceful shutdown error: %v", err)
		}
		close(idleConnsClosed)
	}()

	s.logger.Printf("admin server listening on %s (static=%s)", *flagListen, *flagStatic)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.logger.Fatalf("server error: %v", err)
	}

	<-idleConnsClosed
	s.logger.Printf("server stopped")
}
