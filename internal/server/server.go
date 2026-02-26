package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aytrw/otaku-chart-maker/internal/api"
)

const (
	stateFileName = "state.json"
	coversDirName = "covers"
)

// imageExts 定义 /api/covers 可返回的图片后缀。
var imageExts = map[string]struct{}{
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".webp": {},
	".bmp":  {},
	".gif":  {},
}

// handler 聚合前端文件、状态文件、API 客户端和路由分发所需资源。
type handler struct {
	frontend  fs.FS
	coversDir string
	stateFile string
	bgm       *api.Client
	vndb      *api.VNDBClient
	mux       *http.ServeMux
	stateMu   sync.RWMutex
}

// NewHandler 初始化目录、状态文件和路由，并返回封面数量用于启动信息。
func NewHandler(execDir string, frontend fs.FS) (http.Handler, int, error) {
	if frontend == nil {
		return nil, 0, errors.New("frontend 文件系统不能为空")
	}

	h := &handler{
		frontend:  frontend,
		coversDir: filepath.Join(execDir, coversDirName),
		stateFile: filepath.Join(execDir, stateFileName),
		mux:       http.NewServeMux(),
	}

	if err := os.MkdirAll(h.coversDir, 0o755); err != nil {
		return nil, 0, err
	}

	if _, err := os.Stat(h.stateFile); errors.Is(err, os.ErrNotExist) {
		if writeErr := os.WriteFile(h.stateFile, []byte("{}\n"), 0o644); writeErr != nil {
			return nil, 0, writeErr
		}
	}

	h.bgm = api.NewClient(h.coversDir)
	h.vndb = api.NewVNDBClient(h.coversDir, "")
	h.routes()

	files, err := h.coverFileNames()
	if err != nil {
		return nil, 0, err
	}

	return h, len(files), nil
}

// ServeHTTP 将请求转交给内部 mux。
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// routes 注册所有 HTTP 路由。
func (h *handler) routes() {
	h.mux.HandleFunc("/", h.handleIndex)
	h.mux.Handle("/covers/", http.StripPrefix("/covers/", http.FileServer(http.Dir(h.coversDir))))
	h.mux.HandleFunc("/api/state", h.handleState)
	h.mux.HandleFunc("/api/covers", h.handleCovers)
	h.mux.HandleFunc("/api/search", h.handleSearch)
	h.mux.HandleFunc("/api/browse", h.handleBrowse)
	h.mux.HandleFunc("/api/download-cover", h.handleDownloadCover)
	h.mux.HandleFunc("/api/upload-cover", h.handleUploadCover)
	h.mux.HandleFunc("/api/vndb/search", h.handleVNDBSearch)
}

// handleIndex 返回前端首页内容。
func (h *handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	b, err := fs.ReadFile(h.frontend, "index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

// handleState 统一处理状态读取和写入。
func (h *handler) handleState(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.loadState(w)
	case http.MethodPost:
		h.saveState(w, r)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// handleCovers 返回 covers 目录下的图片文件名列表。
func (h *handler) handleCovers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	files, err := h.coverFileNames()
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	h.writeJSON(w, http.StatusOK, files)
}

// loadState 读取 state.json，文件缺失或空内容时返回空对象。
func (h *handler) loadState(w http.ResponseWriter) {
	h.stateMu.RLock()
	b, err := os.ReadFile(h.stateFile)
	h.stateMu.RUnlock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			h.writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" {
		h.writeJSON(w, http.StatusOK, map[string]any{})
		return
	}

	var anyJSON any
	if err := json.Unmarshal(b, &anyJSON); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "state.json 不是合法 JSON"})
		return
	}

	h.writeJSONRaw(w, http.StatusOK, b)
}

// saveState 接收 JSON 请求体并格式化写入 state.json。
func (h *handler) saveState(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取请求体失败"})
		return
	}

	var anyJSON any
	if err := json.Unmarshal(body, &anyJSON); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}

	formatted, err := json.MarshalIndent(anyJSON, "", "  ")
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "JSON 序列化失败"})
		return
	}
	formatted = append(formatted, '\n')

	h.stateMu.Lock()
	writeErr := os.WriteFile(h.stateFile, formatted, 0o644)
	h.stateMu.Unlock()

	if writeErr != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": writeErr.Error()})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// coverFileNames 扫描 covers 目录并返回图片文件名（不含子目录）。
func (h *handler) coverFileNames() ([]string, error) {
	entries, err := os.ReadDir(h.coversDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if _, ok := imageExts[ext]; ok {
			files = append(files, e.Name())
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i]) < strings.ToLower(files[j])
	})
	return files, nil
}

// handleSearch 处理关键词搜索请求（POST /api/search）。
func (h *handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Keyword string `json:"keyword"`
		Type    int    `json:"type"`
	}
	if err := readJSON(r, &req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析请求失败"})
		return
	}
	if req.Type == 0 {
		req.Type = 2 // 默认搜索动画
	}

	results, err := h.bgm.Search(req.Keyword, req.Type)
	if err != nil {
		h.writeAPIError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handleBrowse 处理标签浏览请求（POST /api/browse）。
func (h *handler) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req api.BrowseRequest
	if err := readJSON(r, &req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析请求失败"})
		return
	}

	resp, err := h.bgm.Browse(req)
	if err != nil {
		h.writeAPIError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, resp)
}

// handleVNDBSearch 处理 VNDB 关键词搜索请求（POST /api/vndb/search）。
func (h *handler) handleVNDBSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Keyword string `json:"keyword"`
		Page    int    `json:"page"`
	}
	if err := readJSON(r, &req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析请求失败"})
		return
	}
	if req.Page <= 0 {
		req.Page = 1
	}

	resp, err := h.vndb.SearchVN(req.Keyword, req.Page, 20)
	if err != nil {
		h.writeAPIError(w, err)
		return
	}

	// 将 VNDB 结果映射为前端通用的卡片格式
	type card struct {
		ID     string  `json:"id"`
		Name   string  `json:"name"`
		NameCN string  `json:"name_cn"`
		Cover  string  `json:"cover"`
		Score  float64 `json:"score"`
		Source string  `json:"source"`
	}

	cards := make([]card, 0, len(resp.Results))
	for _, vn := range resp.Results {
		cards = append(cards, card{
			ID:     vn.ID,
			Name:   vn.Title,
			NameCN: vn.Alttitle,
			Cover:  vn.Image.BestURL(),
			Score:  vn.Rating / 10,
			Source: "vndb",
		})
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"results": cards,
		"total":   resp.Count,
		"more":    resp.More,
	})
}

// handleDownloadCover 处理封面下载请求（POST /api/download-cover）。
// source 字段可选，值为 "vndb" 时使用 VNDB 客户端下载，否则默认 Bangumi。
func (h *handler) handleDownloadCover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		URL      string `json:"url"`
		Filename string `json:"filename"`
		Source   string `json:"source"`
	}
	if err := readJSON(r, &req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析请求失败"})
		return
	}

	var result *api.DownloadResult
	var err error
	if req.Source == "vndb" {
		result, err = h.vndb.DownloadCover(req.URL, req.Filename)
	} else {
		result, err = h.bgm.DownloadCover(req.URL, req.Filename)
	}
	if err != nil {
		h.writeAPIError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"filename": result.Filename,
		"path":     result.Path,
		"size":     result.Size,
	})
}

// handleUploadCover 接收前端上传的图片文件并保存到 covers 目录。
func (h *handler) handleUploadCover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	const maxUpload = 20 << 20 // 20MB
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "文件过大或解析失败"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少文件"})
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if _, ok := imageExts[ext]; !ok {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "不支持的图片格式"})
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取文件失败"})
		return
	}

	filename := header.Filename
	_ = os.MkdirAll(h.coversDir, 0o755)
	filename = uniqueFilename(h.coversDir, filename)
	savePath := filepath.Join(h.coversDir, filename)
	if err := os.WriteFile(savePath, data, 0o644); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存文件失败"})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"filename": filename,
		"path":     "covers/" + filename,
		"size":     len(data),
	})
}

// uniqueFilename 如果同名文件已存在，加数字后缀避免覆盖。
func uniqueFilename(dir, filename string) string {
	if _, err := os.Stat(filepath.Join(dir, filename)); os.IsNotExist(err) {
		return filename
	}
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	for n := 1; n <= 9999; n++ {
		candidate := fmt.Sprintf("%s_%d%s", base, n, ext)
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
	return fmt.Sprintf("%s_%d%s", base, time.Now().UnixNano(), ext)
}

// readJSON 从请求体解析 JSON 到目标结构。
func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	return json.Unmarshal(body, v)
}

// writeAPIError 将业务错误映射为合适的 HTTP 状态码。
func (h *handler) writeAPIError(w http.ResponseWriter, err error) {
	if api.IsBadRequest(err) {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
}

// writeJSON 将结构体或映射编码后输出为 JSON 响应。
func (h *handler) writeJSON(w http.ResponseWriter, code int, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		http.Error(w, "JSON 编码失败", http.StatusInternalServerError)
		return
	}
	h.writeJSONRawWithCode(w, code, b)
}

// writeJSONRaw 直接输出已编码的 JSON 字节。
func (h *handler) writeJSONRaw(w http.ResponseWriter, code int, data []byte) {
	h.writeJSONRawWithCode(w, code, data)
}

func (h *handler) writeJSONRawWithCode(w http.ResponseWriter, code int, data []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(code)
	_, _ = w.Write(data)
}
