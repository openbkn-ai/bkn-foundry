// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"vega-backend/interfaces"
	"vega-backend/logics"
)

// OwnershipSyncWorker 把「数据表属于哪个数据目录」这件事推给 bkn-safe（#800）。
//
// bkn-safe 只见过不透明的 "type:id" 对象键，无从得知一张表属于哪个目录，所以
// 目录上的授权到不了目录下的表。这件事只有 vega 知道，于是由 vega 推。
//
// 为什么不走 PermissionAccess 端口：给那个接口加方法要连带改端口定义、mock 和
// ISF 那份实现，是在动现有机制。这里自带一个小客户端，多几十行换零波及面。
type OwnershipSyncWorker struct {
	baseURL  string
	http     *http.Client
	interval time.Duration
	// approved 是「我看过预演报告，可以推」的批准记录，不是判定开关。
	// 判定开关已经明确否掉：归属表为空天然等于关闭，回滚是删数据不是发版。
	// 它落在 vega 而不是 bkn-safe，因为决定推不推的是数据的生产方。
	//
	// 它是常驻环境变量，因此批准的其实是「此后每一份快照」，而人只看过当时那
	// 一份。缓解办法不是假装它是一次性的：预演每轮照跑、差异照记日志，扩权因此
	// 始终留痕；用法是收敛完就把它关回去。
	approved bool
	stop     chan struct{}
	once     sync.Once
}

const (
	ownershipSyncDefaultInterval = 30 * time.Minute
	// ownershipSyncChunk 与 bkn-safe 单次请求上限一致。
	ownershipSyncChunk = 1000
	// ownershipPageSize 有两处用途：按批取本地资源详情（IN 子句的长度上限），
	// 以及回读 bkn-safe 归属表时的分页步长——那个端点是真的支持 limit/offset。
	ownershipPageSize = 500

	authResourceType = "resource"
	authParentType   = "catalog"
)

// NewOwnershipSyncWorker 构造同步器。BKN_SAFE_URL 为空即不启用——ISF 授权面
// 没有归属表这个概念，推了也没人读。
func NewOwnershipSyncWorker() *OwnershipSyncWorker {
	interval := ownershipSyncDefaultInterval
	if v := os.Getenv("VEGA_OWNERSHIP_SYNC_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		} else {
			logger.Warnf("忽略非法的 VEGA_OWNERSHIP_SYNC_INTERVAL=%q: %v", v, err)
		}
	}
	return &OwnershipSyncWorker{
		baseURL:  os.Getenv("BKN_SAFE_URL"),
		http:     &http.Client{Timeout: 30 * time.Second},
		interval: interval,
		approved: os.Getenv("VEGA_OWNERSHIP_SYNC_APPROVED") == "true",
		stop:     make(chan struct{}),
	}
}

// Start 启动周期同步。BKN_SAFE_URL 未配置时直接返回，不报错——授权面可能仍是 ISF。
func (w *OwnershipSyncWorker) Start() error {
	if w.baseURL == "" {
		logger.Info("BKN_SAFE_URL 未配置，资源归属同步不启用")
		return nil
	}
	go func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		// 启动即跑一次：新装环境的归属表是空的，越早填上，目录级授权越早能用。
		w.runOnce()
		for {
			select {
			case <-ticker.C:
				w.runOnce()
			case <-w.stop:
				return
			}
		}
	}()
	return nil
}

// Stop 停止同步器。幂等。
func (w *OwnershipSyncWorker) Stop() {
	w.once.Do(func() { close(w.stop) })
}

func (w *OwnershipSyncWorker) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	err := w.sync(ctx)
	switch {
	case err == nil:
	case errors.Is(err, errEndpointAbsent):
		// 对端是老版本。这是允许的组合，说一次就好，不当故障刷屏。
		logger.Info("bkn-safe 尚不支持资源归属登记，本轮同步跳过")
	default:
		logger.Errorf("资源归属同步失败: %v", err)
	}
}

// sync 读本地资源快照 → 逐片预演 → 决定推不推 → 对账清孤儿。
func (w *OwnershipSyncWorker) sync(ctx context.Context) error {
	links, err := w.snapshot(ctx)
	if err != nil {
		return fmt.Errorf("读取资源快照: %w", err)
	}
	return w.pushSnapshot(ctx, links)
}

// pushSnapshot 是同步的后半段，与本地库解耦，便于单独验证推送决策。
func (w *OwnershipSyncWorker) pushSnapshot(ctx context.Context, links []ownershipLink) error {
	if len(links) == 0 {
		logger.Info("资源归属同步：本地没有可推送的资源")
		return w.reconcile(ctx, links)
	}

	pushed, skipped := 0, 0
	for _, chunk := range chunkLinks(links, ownershipSyncChunk) {
		total, sample, err := w.preview(ctx, chunk)
		if err != nil {
			return fmt.Errorf("预演: %w", err)
		}
		if total > 0 {
			if !w.approved {
				// 差异非空意味着有人的可见范围会变。这里停下不是保守，是把决定
				// 权交回给人：报告先出，推送等批准。
				skipped += len(chunk)
				logger.Warnf("资源归属同步暂停：本片会改变 %d 条判定，需人工确认后设置 "+
					"VEGA_OWNERSHIP_SYNC_APPROVED=true 再推。样本: %s", total, sample)
				continue
			}
			// 已批准也照记：批准是对当时那份快照的，之后新建的目录与资源带来的
			// 扩权没人看过，至少要留下痕迹。
			logger.Warnf("资源归属同步（已批准）：本片改变 %d 条判定。样本: %s", total, sample)
		}
		if err := w.push(ctx, chunk); err != nil {
			return fmt.Errorf("推送归属: %w", err)
		}
		pushed += len(chunk)
	}
	logger.Infof("资源归属同步：已推送 %d 条，待确认 %d 条", pushed, skipped)

	return w.reconcile(ctx, links)
}

// ownershipLink 是一条 (资源 -> 所属目录)。
type ownershipLink struct {
	ResourceID string `json:"resource_id"`
	ParentID   string `json:"parent_id"`
}

// snapshot 读出全部「非内部目录下的资源」及其目录。
//
// 跳过内部目录下的资源是有意的：它们在权限服务按 internal_catalog /
// internal_resource 注册，种子里没有声明层级，推过去会被 400 拒掉——那是设计
// 如此，不是故障。它们本来也只有超级管理员通配够得到，继承对其无收益。
//
// 这里不翻页：ResourceAccess.ListIDs 的实现根本不读 Offset/Limit，一次就返回
// 全量 id（drivenadapters/resource/resource_access.go 的 builder 里没有
// Limit/Offset）。写成翻页循环反而会每轮拿到同一批全量、无限追加直到 context
// 超时——同步永远走不到推送那一步。真要分页得先改适配器。
func (w *OwnershipSyncWorker) snapshot(ctx context.Context) ([]ownershipLink, error) {
	internalIDs, err := logics.CA.ListInternalIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取内部目录: %w", err)
	}
	internal := make(map[string]bool, len(internalIDs))
	for _, id := range internalIDs {
		internal[id] = true
	}

	ids, err := logics.RA.ListIDs(ctx, interfaces.ResourcesQueryParams{})
	if err != nil {
		return nil, err
	}
	out := make([]ownershipLink, 0, len(ids))
	// 详情按批取：GetByIDsBasic 拼的是 IN 子句，一次塞进上万个 id 会把语句撑爆。
	// 它不解析 schema/logic 那几个大 JSON 字段，这里只要 catalog_id。
	for start := 0; start < len(ids); start += ownershipPageSize {
		batch := ids[start:min(start+ownershipPageSize, len(ids))]
		resources, err := logics.RA.GetByIDsBasic(ctx, batch)
		if err != nil {
			return nil, err
		}
		for _, r := range resources {
			if r == nil || r.ID == "" || r.CatalogID == "" || internal[r.CatalogID] {
				continue
			}
			out = append(out, ownershipLink{ResourceID: r.ID, ParentID: r.CatalogID})
		}
	}
	return out, nil
}

// preview 问 bkn-safe：这一片推下去，谁的判定会变。返回总数与一条样本。
func (w *OwnershipSyncWorker) preview(ctx context.Context, chunk []ownershipLink) (int, string, error) {
	var resp struct {
		Changes []struct {
			AccessorID string `json:"accessor_id"`
			ResourceID string `json:"resource_id"`
			Operation  string `json:"operation"`
			Direction  string `json:"direction"`
		} `json:"changes"`
		Total int `json:"total"`
	}
	if err := w.call(ctx, http.MethodPut,
		"/api/safe/v1/authz/resource-parents?dry_run=true", chunk, &resp); err != nil {
		return 0, "", err
	}
	sample := ""
	if len(resp.Changes) > 0 {
		c := resp.Changes[0]
		sample = fmt.Sprintf("%s %s %s/%s", c.Direction, c.AccessorID, authResourceType, c.ResourceID) +
			" " + c.Operation
	}
	return resp.Total, sample, nil
}

func (w *OwnershipSyncWorker) push(ctx context.Context, chunk []ownershipLink) error {
	return w.call(ctx, http.MethodPut, "/api/safe/v1/authz/resource-parents", chunk, nil)
}

// reconcile 回读 bkn-safe 存了什么，删掉 vega 已经没有的资源留下的孤儿行。
// 删除路径本身也会删（bkn-safe 在 DELETE /policies 时顺手删），这里是兜底：
// 进程崩溃、跨服务调用失败都会漏，对账才是可靠手段。
func (w *OwnershipSyncWorker) reconcile(ctx context.Context, links []ownershipLink) error {
	live := make(map[string]bool, len(links))
	for _, l := range links {
		live[l.ResourceID] = true
	}
	var orphans []string
	for offset := 0; ; offset += ownershipPageSize {
		var resp struct {
			Items []struct {
				ResourceID string `json:"resource_id"`
			} `json:"items"`
			Total int `json:"total"`
		}
		path := fmt.Sprintf("/api/safe/v1/authz/resource-parents?resource_type=%s&limit=%d&offset=%d",
			authResourceType, ownershipPageSize, offset)
		if err := w.call(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return fmt.Errorf("回读归属: %w", err)
		}
		for _, it := range resp.Items {
			if !live[it.ResourceID] {
				orphans = append(orphans, it.ResourceID)
			}
		}
		if len(resp.Items) < ownershipPageSize {
			break
		}
	}
	if len(orphans) == 0 {
		return nil
	}
	logger.Infof("资源归属对账：清理 %d 条孤儿记录", len(orphans))
	for start := 0; start < len(orphans); start += ownershipSyncChunk {
		end := min(start+ownershipSyncChunk, len(orphans))
		body := map[string]any{
			"resource_type": authResourceType,
			"resource_ids":  orphans[start:end],
		}
		if err := w.callRaw(ctx, http.MethodDelete, "/api/safe/v1/authz/resource-parents", body, nil); err != nil {
			return fmt.Errorf("清理孤儿归属: %w", err)
		}
	}
	return nil
}

// call 发一个带 items 包装的归属请求（PUT 用），或一个无 body 的读请求（GET 用）。
func (w *OwnershipSyncWorker) call(ctx context.Context, method, path string, items []ownershipLink, out any) error {
	var body any
	if items != nil {
		body = map[string]any{
			"resource_type": authResourceType,
			"parent_type":   authParentType,
			"items":         items,
		}
	}
	return w.callRaw(ctx, method, path, body, out)
}

func (w *OwnershipSyncWorker) callRaw(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, w.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		// 老版 bkn-safe 没有这个端点。版本错配必须容错跳过，不能刷屏也不能崩。
		return errEndpointAbsent
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bkn-safe %s %s: %d %s", method, path, resp.StatusCode, string(raw))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// errEndpointAbsent 标记「对端还没有这个能力」。新版 vega 配老版 bkn-safe 是
// 允许的组合，此时同步器安静退出，不把版本错配变成一串报错。
var errEndpointAbsent = errors.New("bkn-safe 尚不支持资源归属登记")

// chunkLinks 把快照切成不超过 size 的片。分片是为了让单次请求的事务与内存有界，
// 而不是随一个部署的资源总量增长。
func chunkLinks(links []ownershipLink, size int) [][]ownershipLink {
	if size <= 0 || len(links) == 0 {
		return nil
	}
	out := make([][]ownershipLink, 0, (len(links)+size-1)/size)
	for start := 0; start < len(links); start += size {
		out = append(out, links[start:min(start+size, len(links))])
	}
	return out
}
