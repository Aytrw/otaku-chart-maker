package api

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// VNDB Kana v2 相关配置常量。
const (
	vndbBaseURL         = "https://api.vndb.org/kana"
	vndbVNURL           = vndbBaseURL + "/vn"
	vndbStatsURL        = vndbBaseURL + "/stats"
	vndbAuthInfoURL     = vndbBaseURL + "/authinfo"
	vndbSchemaURL       = vndbBaseURL + "/schema"
	vndbUserAgent       = "OtakuChartMaker/1.0 (https://github.com/Aytrw/otaku-chart-maker)"
	vndbCacheTTL        = 5 * time.Minute
	vndbCacheCleanTick  = 1 * time.Minute
	vndbCacheMaxEntries = 800
	vndbDefaultResults  = 20
	vndbMaxResults      = 100
)

// VNDBClient 是 VNDB Kana v2 API 客户端。
type VNDBClient struct {
	http      *http.Client
	token     string
	coversDir string
	mu        sync.Mutex
	cache     map[string]vndbCacheEntry
}

// vndbCacheEntry 是 VNDB 客户端缓存条目。
type vndbCacheEntry struct {
	data   []byte
	expire time.Time
	added  time.Time
}

// VNDBQueryRequest 定义 Kana v2 查询请求体。
type VNDBQueryRequest struct {
	Filters           any    `json:"filters,omitempty"`
	Fields            string `json:"fields,omitempty"`
	Sort              string `json:"sort,omitempty"`
	Reverse           bool   `json:"reverse,omitempty"`
	Results           int    `json:"results,omitempty"`
	Page              int    `json:"page,omitempty"`
	Count             bool   `json:"count,omitempty"`
	CompactFilters    bool   `json:"compact_filters,omitempty"`
	NormalizedFilters bool   `json:"normalized_filters,omitempty"`
}

// VNDBVN 是视觉小说基础数据结构。
type VNDBVN struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Alttitle string    `json:"alttitle"`
	Image    VNDBImage `json:"image"`
	Rating   float64   `json:"rating"`
	Released string    `json:"released"`
}

// VNDBImage 是 VNDB 图片字段。
type VNDBImage struct {
	URL           string `json:"url"`
	Dims          []int  `json:"dims"`
	ThumbnailURL  string `json:"thumbnail"`
	ThumbnailDims []int  `json:"thumbnail_dims"`
}

// BestURL 返回最可用的图片地址。
func (img VNDBImage) BestURL() string {
	if img.URL != "" {
		return img.URL
	}
	return img.ThumbnailURL
}

// VNDBQueryResponse 定义 Kana v2 查询响应体。
type VNDBQueryResponse struct {
	Results []VNDBVN `json:"results"`
	More    bool     `json:"more"`
	Count   int      `json:"count"`
}

// VNDBStats 是 /stats 接口返回值。
type VNDBStats struct {
	Chars     int `json:"chars"`
	Producers int `json:"producers"`
	Releases  int `json:"releases"`
	Staff     int `json:"staff"`
	Tags      int `json:"tags"`
	Traits    int `json:"traits"`
	VN        int `json:"vn"`
}

// VNDBAuthInfo 是 /authinfo 接口返回值。
type VNDBAuthInfo struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Permissions []string `json:"permissions"`
}

// NewVNDBClient 创建 VNDB API 客户端。
func NewVNDBClient(coversDir, token string) *VNDBClient {
	c := &VNDBClient{
		http:      &http.Client{Timeout: 15 * time.Second},
		token:     strings.TrimSpace(token),
		coversDir: coversDir,
		cache:     make(map[string]vndbCacheEntry),
	}
	go c.startCacheCleaner()
	return c
}

// SetToken 更新客户端鉴权 Token。
func (c *VNDBClient) SetToken(token string) {
	c.mu.Lock()
	c.token = strings.TrimSpace(token)
	c.mu.Unlock()
}

// QueryVN 按 Kana v2 格式查询视觉小说。
func (c *VNDBClient) QueryVN(req VNDBQueryRequest) (*VNDBQueryResponse, error) {
	if req.Results <= 0 || req.Results > vndbMaxResults {
		req.Results = vndbDefaultResults
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if strings.TrimSpace(req.Fields) == "" {
		req.Fields = "id,title,alttitle,image.url,image.thumbnail,rating,released"
	}
	if strings.TrimSpace(req.Sort) == "" {
		req.Sort = "id"
	}

	body, err := c.cachedPost(vndbVNURL, req)
	if err != nil {
		return nil, err
	}

	var resp VNDBQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析 VNDB 响应失败: %w", err)
	}
	return &resp, nil
}

// SearchVN 使用关键词进行视觉小说搜索。
func (c *VNDBClient) SearchVN(keyword string, page, results int) (*VNDBQueryResponse, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, badRequestError("关键词不能为空")
	}

	req := VNDBQueryRequest{
		Filters: []any{"search", "=", keyword},
		Fields:  "id,title,alttitle,image.url,image.thumbnail,rating,released",
		Sort:    "searchrank",
		Results: results,
		Page:    page,
		Count:   true,
	}
	return c.QueryVN(req)
}

// GetStats 获取 VNDB 数据库统计信息。
func (c *VNDBClient) GetStats() (*VNDBStats, error) {
	body, err := c.get(vndbStatsURL, false)
	if err != nil {
		return nil, err
	}

	var stats VNDBStats
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("解析 stats 失败: %w", err)
	}
	return &stats, nil
}

// GetSchema 获取 VNDB Kana schema 元数据。
func (c *VNDBClient) GetSchema() (map[string]any, error) {
	body, err := c.get(vndbSchemaURL, false)
	if err != nil {
		return nil, err
	}

	var schema map[string]any
	if err := json.Unmarshal(body, &schema); err != nil {
		return nil, fmt.Errorf("解析 schema 失败: %w", err)
	}
	return schema, nil
}

// GetAuthInfo 校验当前 Token 并返回权限信息。
func (c *VNDBClient) GetAuthInfo() (*VNDBAuthInfo, error) {
	if strings.TrimSpace(c.token) == "" {
		return nil, badRequestError("缺少 VNDB API Token")
	}

	body, err := c.get(vndbAuthInfoURL, true)
	if err != nil {
		return nil, err
	}

	var info VNDBAuthInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("解析 authinfo 失败: %w", err)
	}
	return &info, nil
}

// DownloadCover 下载 VNDB 封面到本地 covers 目录。
func (c *VNDBClient) DownloadCover(imgURL, filename string) (*DownloadResult, error) {
	imgURL = strings.TrimSpace(imgURL)
	if imgURL == "" {
		return nil, badRequestError("缺少图片 URL")
	}

	filename = sanitizeFilename(imgURL, filename)

	// 同名封面已存在则直接复用，跳过重复下载
	if existing := findExistingCover(c.coversDir, filename); existing != nil {
		return existing, nil
	}
	req, err := http.NewRequest(http.MethodGet, imgURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构建请求失败: %w", err)
	}
	req.Header.Set("User-Agent", vndbUserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载失败 HTTP %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		return nil, fmt.Errorf("非图片类型: %s", ct)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取图片失败: %w", err)
	}

	filename = fixExtByContentType(filename, ct)
	filename = UniqueFilename(c.coversDir, filename)
	_ = os.MkdirAll(c.coversDir, 0o755)
	savePath := filepath.Join(c.coversDir, filename)
	if err := os.WriteFile(savePath, data, 0o644); err != nil {
		return nil, fmt.Errorf("保存封面失败: %w", err)
	}

	return &DownloadResult{
		Filename: filename,
		Path:     "covers/" + filename,
		Size:     len(data),
	}, nil
}

// get 发送 GET 请求并返回响应字节。
func (c *VNDBClient) get(apiURL string, needAuth bool) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	c.applyHeaders(req, needAuth)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("VNDB API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	return c.readAPIResponse(resp)
}

// post 发送 POST 请求并返回响应字节。
func (c *VNDBClient) post(apiURL string, bodyJSON []byte, needAuth bool) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	c.applyHeaders(req, needAuth)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("VNDB API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	return c.readAPIResponse(resp)
}

// cachedPost 执行带缓存的 POST 请求。
func (c *VNDBClient) cachedPost(apiURL string, body any) ([]byte, error) {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	key := c.makeCacheKey(apiURL, bodyJSON)
	now := time.Now()

	c.mu.Lock()
	if entry, ok := c.cache[key]; ok {
		if now.Before(entry.expire) {
			c.mu.Unlock()
			return entry.data, nil
		}
		delete(c.cache, key)
	}
	c.mu.Unlock()

	result, err := c.post(apiURL, bodyJSON, false)
	if err != nil {
		return nil, err
	}

	cachedAt := time.Now()
	c.mu.Lock()
	c.cache[key] = vndbCacheEntry{data: result, expire: cachedAt.Add(vndbCacheTTL), added: cachedAt}
	c.pruneExpiredLocked(cachedAt)
	c.evictOverflowLocked()
	c.mu.Unlock()

	return result, nil
}

// applyHeaders 设置 VNDB 请求所需公共请求头。
func (c *VNDBClient) applyHeaders(req *http.Request, needAuth bool) {
	req.Header.Set("User-Agent", vndbUserAgent)
	req.Header.Set("Accept", "application/json")
	if !needAuth {
		return
	}

	c.mu.Lock()
	token := c.token
	c.mu.Unlock()
	if token != "" {
		req.Header.Set("Authorization", "Token "+token)
	}
}

// readAPIResponse 统一处理 HTTP 状态码和错误文本。
func (c *VNDBClient) readAPIResponse(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return body, nil
	}

	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = fmt.Sprintf("VNDB API 错误 %d", resp.StatusCode)
	}

	switch resp.StatusCode {
	case http.StatusBadRequest:
		return nil, badRequestError(msg)
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("VNDB 认证失败: %s", msg)
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("VNDB 请求过于频繁: %s", msg)
	default:
		return nil, fmt.Errorf("VNDB API 错误 %d: %s", resp.StatusCode, msg)
	}
}

// makeCacheKey 使用 URL 与请求体生成缓存键。
func (c *VNDBClient) makeCacheKey(apiURL string, bodyJSON []byte) string {
	h := md5.New()
	h.Write([]byte(apiURL))
	h.Write(bodyJSON)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// startCacheCleaner 周期清理过期缓存。
func (c *VNDBClient) startCacheCleaner() {
	ticker := time.NewTicker(vndbCacheCleanTick)
	for now := range ticker.C {
		c.mu.Lock()
		c.pruneExpiredLocked(now)
		c.evictOverflowLocked()
		c.mu.Unlock()
	}
}

// pruneExpiredLocked 清理过期缓存条目。
func (c *VNDBClient) pruneExpiredLocked(now time.Time) {
	for key, entry := range c.cache {
		if !now.Before(entry.expire) {
			delete(c.cache, key)
		}
	}
}

// evictOverflowLocked 当缓存超过上限时批量淘汰最早加入的条目（调用方需持锁）。
func (c *VNDBClient) evictOverflowLocked() {
	excess := len(c.cache) - vndbCacheMaxEntries
	if excess <= 0 {
		return
	}
	victims := make([]struct {
		key   string
		added time.Time
	}, 0, excess)
	for key, entry := range c.cache {
		if len(victims) < excess {
			victims = append(victims, struct {
				key   string
				added time.Time
			}{key, entry.added})
		} else {
			newest := 0
			for vi := 1; vi < len(victims); vi++ {
				if victims[vi].added.After(victims[newest].added) {
					newest = vi
				}
			}
			if entry.added.Before(victims[newest].added) {
				victims[newest] = struct {
					key   string
					added time.Time
				}{key, entry.added}
			}
		}
	}
	for _, v := range victims {
		delete(c.cache, v.key)
	}
}

// EnsureVNDBClient 用于提前暴露客户端构造能力给上层检查。
func EnsureVNDBClient(c *VNDBClient) error {
	if c == nil {
		return errors.New("vndb client 不能为空")
	}
	return nil
}
