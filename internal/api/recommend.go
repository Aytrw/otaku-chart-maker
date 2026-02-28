package api

import (
	"sort"
	"strings"
	"sync"
)

// RecommendCellSpec 描述单个格子的推荐参数。
// Tags/Sort/SubjectType 决定查询条件，Offset 用于"换一个"时跳过已选项。
type RecommendCellSpec struct {
	Label       string   `json:"label"`
	Tags        []string `json:"tags"`
	Sort        string   `json:"sort"`
	SubjectType string   `json:"subjectType"`
	Offset      int      `json:"offset"`
}

// RecommendRequest 是批量推荐请求。
type RecommendRequest struct {
	Cells      []RecommendCellSpec `json:"cells"`
	ExcludeIDs []int               `json:"excludeIDs"`
}

// RecommendCellResult 是单个格子的推荐结果。
type RecommendCellResult struct {
	Label string        `json:"label"`
	Item  *BrowseResult `json:"item,omitempty"`
	Found bool          `json:"found"`
}

// RecommendResponse 是批量推荐响应。
type RecommendResponse struct {
	Results []RecommendCellResult `json:"results"`
}

// recommendConcurrency 控制并发请求 Bangumi API 的最大 goroutine 数量。
const recommendConcurrency = 8

// recommendQueryKey 用于合并相同查询参数的 API 请求。
type recommendQueryKey struct {
	tags        string
	sortBy      string
	subjectType string
}

// makeRecommendKey 根据推荐参数生成查询分组键。
// 相同键的格子共享一次 API 请求，节省网络开销。
func makeRecommendKey(spec RecommendCellSpec) recommendQueryKey {
	sorted := make([]string, len(spec.Tags))
	copy(sorted, spec.Tags)
	sort.Strings(sorted)
	subjectType := spec.SubjectType
	if len(spec.Tags) == 0 && subjectType == "" {
		subjectType = "anime"
	}
	return recommendQueryKey{
		tags:        strings.Join(sorted, "\x00"),
		sortBy:      spec.Sort,
		subjectType: subjectType,
	}
}

// Recommend 批量为多个格子推荐作品。
// 相同查询条件的格子共享一次 API 请求，结果全局去重。
func (c *Client) Recommend(req RecommendRequest) (*RecommendResponse, error) {
	if len(req.Cells) == 0 {
		return &RecommendResponse{Results: []RecommendCellResult{}}, nil
	}

	// 1. 按查询条件分组，相同 (tags, sort, type) 的格子共享一次请求
	type groupInfo struct {
		key     recommendQueryKey
		indices []int
	}
	groupMap := map[recommendQueryKey]*groupInfo{}
	for i, spec := range req.Cells {
		key := makeRecommendKey(spec)
		g, ok := groupMap[key]
		if !ok {
			g = &groupInfo{key: key}
			groupMap[key] = g
		}
		g.indices = append(g.indices, i)
	}

	// 2. 并发请求每个分组（信号量控制并发数）
	type fetchResult struct {
		key     recommendQueryKey
		results []BrowseResult
	}
	ch := make(chan fetchResult, len(groupMap))
	sem := make(chan struct{}, recommendConcurrency)
	var wg sync.WaitGroup

	for _, g := range groupMap {
		wg.Add(1)
		go func(info *groupInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var tags []string
			if info.key.tags != "" {
				tags = strings.Split(info.key.tags, "\x00")
			}
			browseReq := BrowseRequest{
				Tags:        tags,
				Sort:        info.key.sortBy,
				SubjectType: info.key.subjectType,
				Limit:       maxBrowseLimit,
			}
			resp, err := c.Browse(browseReq)
			if err == nil && resp != nil {
				ch <- fetchResult{info.key, resp.Results}
			} else {
				ch <- fetchResult{info.key, nil}
			}
		}(g)
	}
	go func() { wg.Wait(); close(ch) }()

	// 收集各分组的查询结果
	pool := map[recommendQueryKey][]BrowseResult{}
	for fr := range ch {
		if fr.results != nil {
			pool[fr.key] = fr.results
		}
	}

	// 3. 全局去重分配：按格子顺序依次从对应池中取未使用的结果
	usedIDs := map[int]bool{}
	for _, id := range req.ExcludeIDs {
		usedIDs[id] = true
	}

	results := make([]RecommendCellResult, len(req.Cells))
	for i, spec := range req.Cells {
		results[i] = RecommendCellResult{Label: spec.Label}
		key := makeRecommendKey(spec)
		items, ok := pool[key]
		if !ok || len(items) == 0 {
			continue
		}

		// 遍历结果池：跳过已使用的 ID，再跳过 offset 个有效结果
		skipped := 0
		for _, item := range items {
			if usedIDs[item.ID] {
				continue
			}
			if skipped < spec.Offset {
				skipped++
				continue
			}
			itemCopy := item
			results[i].Item = &itemCopy
			results[i].Found = true
			usedIDs[itemCopy.ID] = true
			break
		}
	}

	return &RecommendResponse{Results: results}, nil
}
