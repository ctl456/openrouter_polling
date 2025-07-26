package healthcheck

import (
	"context"
	"io"
	"net/http"
	"openrouter_polling/apimanager"
	"openrouter_polling/config"
	"openrouter_polling/utils"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	Log       *logrus.Logger
	ApiKeyMgr *apimanager.ApiKeyManager
)

func PerformPeriodicHealthChecks(ctx context.Context) {
	initialDelay := 15 * time.Second
	select {
	case <-time.After(initialDelay):
	case <-ctx.Done():
		Log.Info("健康检查任务在初始延迟期间被父上下文取消。")
		return
	}

	Log.Info("启动 OpenRouter API 密钥的定期健康检查任务。")
	ticker := time.NewTicker(config.AppSettings.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			Log.Info("健康检查任务因父上下文取消而停止。")
			return
		case <-ticker.C:
			Log.Debug("健康检查: 运行计划中的 API 密钥健康检查周期...")

			// 从管理器获取内存中密钥状态的快照
			keysToCheckSnapshot := ApiKeyMgr.GetCachedKeys()

			if len(keysToCheckSnapshot) == 0 {
				Log.Debug("健康检查: 当前没有需要主动检查的密钥。")
				continue
			}
			Log.Debugf("健康检查: 快照中包含 %d 个候选密钥，将逐个评估其当前状态。", len(keysToCheckSnapshot))

			healthCheckClient := &http.Client{
				Timeout: 15 * time.Second,
			}

			checkedCount := 0
			for _, ks := range keysToCheckSnapshot {
				// 确定是否需要对当前密钥进行主动健康检查
				isCandidateForCheck := !ks.IsActive || ks.FailureCount > 0
				if !isCandidateForCheck {
					continue
				}

				Log.Infof("健康检查: 主动检查密钥 %s (当前状态: Active=%t, Failures=%d, CoolingUntil=%v)",
					utils.SafeSuffix(ks.Key), ks.IsActive, ks.FailureCount, ks.CoolDownUntil)
				checkedCount++

				hcCtx, hcCancel := context.WithTimeout(ctx, healthCheckClient.Timeout)

				req, err := http.NewRequestWithContext(hcCtx, "GET", config.AppSettings.OpenRouterModelsURL, nil)
				if err != nil {
					Log.Errorf("健康检查: 为密钥 %s 创建请求失败: %v。", utils.SafeSuffix(ks.Key), err)
					hcCancel()
					continue
				}
				req.Header.Set("Authorization", "Bearer "+ks.Key)

				resp, err := healthCheckClient.Do(req)
				hcCancel()

				if err != nil {
					Log.Warnf("健康检查: 密钥 %s 的请求失败: %v。", utils.SafeSuffix(ks.Key), err)
					if hcCtx.Err() == context.DeadlineExceeded || (err != nil && (strings.Contains(strings.ToLower(err.Error()), "timeout") || strings.Contains(strings.ToLower(err.Error()), "deadline exceeded"))) {
						Log.Warnf("健康检查: 密钥 %s 因超时失败。", utils.SafeSuffix(ks.Key))
						ApiKeyMgr.MarkKeyFailure(ks.Key)
					}
					continue
				}

				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				if resp.StatusCode == http.StatusOK {
					Log.Infof("健康检查: 密钥 %s 通过健康检查 (状态 %d)。", utils.SafeSuffix(ks.Key), resp.StatusCode)
					ApiKeyMgr.RecordKeySuccess(ks.Key)
				} else if resp.StatusCode == http.StatusUnauthorized ||
					resp.StatusCode == http.StatusForbidden ||
					resp.StatusCode == http.StatusTooManyRequests {
					Log.Warnf("健康检查: 密钥 %s 验证失败，返回状态 %d。", utils.SafeSuffix(ks.Key), resp.StatusCode)
					ApiKeyMgr.MarkKeyFailure(ks.Key)
				} else {
					Log.Warnf("健康检查: 密钥 %s 的请求返回非预期状态 %d。健康检查暂不改变其状态。",
						utils.SafeSuffix(ks.Key), resp.StatusCode)
				}
			}
			if checkedCount > 0 {
				Log.Debugf("健康检查: 本周期主动检查了 %d 个密钥。", checkedCount)
			} else {
				Log.Debug("健康检查: 本周期没有密钥符合主动检查的条件。")
			}
			Log.Debug("健康检查: API 密钥健康检查周期完成。")
		}
	}
}
