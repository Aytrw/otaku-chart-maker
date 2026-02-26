package api

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Bangumi API 地址和请求参数。
const (
	bgmUserAgent   = "ACGNTable/1.0 (https://github.com/acgn-table)"
	bgmV0SearchURL = "https://api.bgm.tv/v0/search/subjects"
	bgmLegacyURL   = "https://api.bgm.tv/search/subject/"
	cacheTTL       = 5 * time.Minute
	cacheCleanTick = 1 * time.Minute
	cacheMaxEntries = 800
	defaultLimit   = 20
	maxBrowseLimit = 100
)

// ErrBadRequest 表示调用参数无效，应返回 4xx。
var ErrBadRequest = errors.New("bad request")

// requestError 用于保留原始错误信息并附带错误分类。
type requestError struct {
	msg string
}

func (e requestError) Error() string {
	return e.msg
}

func (e requestError) Unwrap() error {
	return ErrBadRequest
}

// SubjectType 描述 Bangumi 条目类型的筛选参数。
type SubjectType struct {
	TypeID  int    // Bangumi 类型 ID: 1=书籍 2=动画 4=游戏
	MetaTag string // 子类标签，为空表示不筛选子类
}

// TypeMap 将前端类型名映射到 Bangumi 条目类型和子类标签。
var TypeMap = map[string]SubjectType{
	"anime":   {TypeID: 2},
	"manga":   {TypeID: 1, MetaTag: "漫画"},
	"novel":   {TypeID: 1, MetaTag: "轻小说"},
	"galgame": {TypeID: 4, MetaTag: "Galgame"},
}

// TypeLabels 将 Bangumi 类型 ID 映射到中文显示名。
var TypeLabels = map[int]string{
	1: "书籍", 2: "动画", 3: "音乐", 4: "游戏", 6: "三次元",
}

// 封面下载允许的图片扩展名。
var coverExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".webp": true, ".bmp": true, ".gif": true,
}

// 文件名中不允许的字符。
var unsafeChars = regexp.MustCompile(`[<>:"/\\|?*]`)

// 浏览接口允许的排序方式。
var validSorts = map[string]bool{
	"rank": true, "score": true, "heat": true, "match": true,
}

// Client 是 Bangumi API 客户端，内含 HTTP 客户端和浏览结果缓存。
type Client struct {
	http      *http.Client
	coversDir string
	mu        sync.Mutex
	cache     map[string]cacheEntry
}

// cacheEntry 是缓存中的一条记录（原始 JSON + 过期时间）。
type cacheEntry struct {
	data   []byte
	expire time.Time
	added  time.Time
}

// NewClient 创建 Bangumi 客户端。coversDir 是封面图片保存目录。
func NewClient(coversDir string) *Client {
	c := &Client{
		http:      &http.Client{Timeout: 15 * time.Second},
		coversDir: coversDir,
		cache:     make(map[string]cacheEntry),
	}
	go c.startCacheCleaner()
	return c
}

// IsBadRequest 判断错误是否属于参数校验类错误。
func IsBadRequest(err error) bool {
	return errors.Is(err, ErrBadRequest)
}

// badRequestError 构建带分类的参数错误。
func badRequestError(msg string) error {
	return requestError{msg: msg}
}

// ---- 关键词搜索 ----

// SearchResult 表示一条搜索结果。
type SearchResult struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	NameCN  string `json:"name_cn"`
	Cover   string `json:"cover"`
	Summary string `json:"summary"`
}

// Search 通过 Bangumi 旧版 API 搜索关键词。bgmType: 1=书籍 2=动画 4=游戏。
func (c *Client) Search(keyword string, bgmType int) ([]SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, badRequestError("关键词不能为空")
	}

	apiURL := bgmLegacyURL + url.PathEscape(keyword) +
		fmt.Sprintf("?type=%d&responseGroup=small&max_results=25", bgmType)

	body, err := c.bgmGet(apiURL)
	if err != nil {
		return nil, err
	}

	// 解析旧版 API 响应格式
	var raw struct {
		List []struct {
			ID      int       `json:"id"`
			Name    string    `json:"name"`
			NameCN  string    `json:"name_cn"`
			Summary string    `json:"summary"`
			Images  bgmImages `json:"images"`
		} `json:"list"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析搜索结果失败: %w", err)
	}

	results := make([]SearchResult, 0, len(raw.List))
	for _, it := range raw.List {
		results = append(results, SearchResult{
			ID:      it.ID,
			Name:    it.Name,
			NameCN:  it.NameCN,
			Cover:   it.Images.bestURL(),
			Summary: truncateRunes(it.Summary, 80),
		})
	}
	return results, nil
}

// ---- 标签浏览 ----

// BrowseRequest 是浏览接口的请求参数。
type BrowseRequest struct {
	Tags        []string `json:"tags"`
	Keyword     string   `json:"keyword"`
	Offset      int      `json:"offset"`
	Limit       int      `json:"limit"`
	Sort        string   `json:"sort"`
	Order       string   `json:"order"`
	SubjectType string   `json:"subjectType"`
}

// BrowseResult 表示一条浏览结果。
type BrowseResult struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	NameCN    string  `json:"name_cn"`
	Cover     string  `json:"cover"`
	TypeLabel string  `json:"type_label"`
	Score     float64 `json:"score"`
}

// BrowseResponse 是浏览接口的响应。
type BrowseResponse struct {
	Results []BrowseResult `json:"results"`
	Total   int            `json:"total"`
	Offset  int            `json:"offset"`
	Limit   int            `json:"limit"`
}

// Browse 通过 Bangumi v0 API 按标签/关键词浏览条目。
func (c *Client) Browse(req BrowseRequest) (*BrowseResponse, error) {
	// 规范化参数
	req.Keyword = strings.TrimSpace(req.Keyword)
	if req.Limit <= 0 || req.Limit > maxBrowseLimit {
		req.Limit = defaultLimit
	}
	if !validSorts[req.Sort] {
		req.Sort = "rank"
	}
	if req.Order != "asc" && req.Order != "desc" {
		req.Order = "desc"
	}

	// 校验：至少要有标签、关键词或类型之一
	st, hasType := TypeMap[req.SubjectType]
	hasTags := len(req.Tags) > 0
	hasKeyword := req.Keyword != ""
	if !hasTags && !hasKeyword && !hasType {
		return nil, badRequestError("请选择题材标签、输入关键词或指定作品类型")
	}

	// 构建 Bangumi v0 请求体
	apiBody := map[string]any{"sort": req.Sort}
	if hasKeyword {
		apiBody["keyword"] = req.Keyword
	}
	filter := map[string]any{}
	if hasTags {
		filter["tag"] = req.Tags
	}
	if hasType {
		filter["type"] = []int{st.TypeID}
		if st.MetaTag != "" {
			filter["meta_tags"] = []string{st.MetaTag}
		}
	}
	if len(filter) > 0 {
		apiBody["filter"] = filter
	}

	// 请求 API（带缓存）
	apiURL := fmt.Sprintf("%s?limit=%d&offset=%d", bgmV0SearchURL, req.Limit, req.Offset)
	rawJSON, err := c.cachedPost(apiURL, apiBody)
	if err != nil {
		return nil, err
	}

	// 解析 v0 API 响应格式
	var raw struct {
		Total int `json:"total"`
		Data  []struct {
			ID     int       `json:"id"`
			Name   string    `json:"name"`
			NameCN string    `json:"name_cn"`
			Images bgmImages `json:"images"`
			Type   int       `json:"type"`
			Score  float64   `json:"score"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rawJSON, &raw); err != nil {
		return nil, fmt.Errorf("解析浏览结果失败: %w", err)
	}

	results := make([]BrowseResult, 0, len(raw.Data))
	for _, it := range raw.Data {
		results = append(results, BrowseResult{
			ID:        it.ID,
			Name:      it.Name,
			NameCN:    it.NameCN,
			Cover:     it.Images.bestURL(),
			TypeLabel: TypeLabels[it.Type],
			Score:     it.Score,
		})
	}

	// Bangumi 返回默认是降序，升序时本地反转当前页。
	if req.Order == "asc" {
		slices.Reverse(results)
	}

	return &BrowseResponse{
		Results: results,
		Total:   raw.Total,
		Offset:  req.Offset,
		Limit:   req.Limit,
	}, nil
}

// ---- 封面下载 ----

// DownloadResult 是封面下载的返回信息。
type DownloadResult struct {
	Filename string `json:"filename"`
	Path     string `json:"path"`
	Size     int    `json:"size"`
}

// DownloadCover 下载远程封面图片到 covers 目录。
func (c *Client) DownloadCover(imgURL, filename string) (*DownloadResult, error) {
	imgURL = strings.TrimSpace(imgURL)
	if imgURL == "" {
		return nil, badRequestError("缺少图片 URL")
	}
	filename = sanitizeFilename(imgURL, filename)

	// 下载图片
	req, err := http.NewRequest("GET", imgURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构建请求失败: %w", err)
	}
	req.Header.Set("User-Agent", bgmUserAgent)
	req.Header.Set("Referer", "https://bgm.tv/")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载失败 HTTP %d", resp.StatusCode)
	}

	imgData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取图片失败: %w", err)
	}

	// 根据 Content-Type 修正扩展名，避免覆盖同名文件
	filename = fixExtByContentType(filename, resp.Header.Get("Content-Type"))
	filename = uniqueFilename(c.coversDir, filename)

	// 写入文件
	_ = os.MkdirAll(c.coversDir, 0o755)
	savePath := filepath.Join(c.coversDir, filename)
	if err := os.WriteFile(savePath, imgData, 0o644); err != nil {
		return nil, fmt.Errorf("保存封面失败: %w", err)
	}

	return &DownloadResult{
		Filename: filename,
		Path:     "covers/" + filename,
		Size:     len(imgData),
	}, nil
}

// ---- Bangumi HTTP 请求 ----

// bgmImages 是 Bangumi 条目的图片字段。
type bgmImages struct {
	Common string `json:"common"`
	Large  string `json:"large"`
	Medium string `json:"medium"`
}

// bestURL 按优先级选取封面 URL（common > large > medium），并强制 https。
func (img bgmImages) bestURL() string {
	cover := img.Common
	if cover == "" {
		cover = img.Large
	}
	if cover == "" {
		cover = img.Medium
	}
	if strings.HasPrefix(cover, "http://") {
		cover = "https://" + cover[7:]
	}
	return cover
}

// bgmGet 向 Bangumi API 发送 GET 请求。
func (c *Client) bgmGet(apiURL string) ([]byte, error) {
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", bgmUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Bangumi API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bangumi API 错误 %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// bgmPost 向 Bangumi API 发送 POST 请求（接收已编码的 JSON 字节）。
func (c *Client) bgmPost(apiURL string, bodyJSON []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", bgmUserAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Bangumi API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bangumi API 错误 %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// ---- 缓存 ----

// cachedPost 带缓存的 POST 请求（命中则直接返回，未命中则请求后存入缓存）。
func (c *Client) cachedPost(apiURL string, body any) ([]byte, error) {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	key := makeCacheKey(apiURL, bodyJSON)

	// 查缓存
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

	// 请求 API 并写入缓存
	result, err := c.bgmPost(apiURL, bodyJSON)
	if err != nil {
		return nil, err
	}

	cachedAt := time.Now()
	c.mu.Lock()
	c.cache[key] = cacheEntry{data: result, expire: cachedAt.Add(cacheTTL), added: cachedAt}
	c.pruneExpiredLocked(cachedAt)
	c.evictOverflowLocked()
	c.mu.Unlock()

	return result, nil
}

// startCacheCleaner 周期清理过期缓存，避免长期运行时缓存膨胀。
func (c *Client) startCacheCleaner() {
	ticker := time.NewTicker(cacheCleanTick)
	for now := range ticker.C {
		c.mu.Lock()
		c.pruneExpiredLocked(now)
		c.evictOverflowLocked()
		c.mu.Unlock()
	}
}

// pruneExpiredLocked 清理所有过期缓存条目（调用方需持锁）。
func (c *Client) pruneExpiredLocked(now time.Time) {
	for key, entry := range c.cache {
		if !now.Before(entry.expire) {
			delete(c.cache, key)
		}
	}
}

// evictOverflowLocked 当缓存超过上限时按最早加入顺序淘汰（调用方需持锁）。
func (c *Client) evictOverflowLocked() {
	for len(c.cache) > cacheMaxEntries {
		var oldestKey string
		var oldestAt time.Time
		found := false
		for key, entry := range c.cache {
			if !found || entry.added.Before(oldestAt) {
				oldestKey = key
				oldestAt = entry.added
				found = true
			}
		}
		if !found {
			return
		}
		delete(c.cache, oldestKey)
	}
}

// makeCacheKey 用 URL + 请求体的 MD5 生成缓存键。
func makeCacheKey(apiURL string, bodyJSON []byte) string {
	h := md5.New()
	h.Write([]byte(apiURL))
	h.Write(bodyJSON)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// ---- 文件名工具 ----

// sanitizeFilename 清理文件名：从 URL 提取、去除不安全字符、确保有图片扩展名。
func sanitizeFilename(imgURL, filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		parts := strings.Split(imgURL, "/")
		filename = strings.Split(parts[len(parts)-1], "?")[0]
	}
	filename = unsafeChars.ReplaceAllString(filename, "_")
	if filename == "" {
		filename = "cover"
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !coverExts[ext] {
		filename += ".jpg"
	}
	return filename
}

// fixExtByContentType 根据 Content-Type 修正文件扩展名（png/webp）。
func fixExtByContentType(filename, contentType string) string {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if strings.Contains(contentType, "png") && !strings.HasSuffix(filename, ".png") {
		return base + ".png"
	}
	if strings.Contains(contentType, "webp") && !strings.HasSuffix(filename, ".webp") {
		return base + ".webp"
	}
	return filename
}

// uniqueFilename 如果同名文件已存在，加数字后缀避免覆盖。
func uniqueFilename(dir, filename string) string {
	if _, err := os.Stat(filepath.Join(dir, filename)); os.IsNotExist(err) {
		return filename
	}
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	for n := 1; ; n++ {
		candidate := fmt.Sprintf("%s_%d%s", base, n, ext)
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}

// truncateRunes 截断字符串到指定 rune 长度。
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}
