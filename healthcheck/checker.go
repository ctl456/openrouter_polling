package healthcheck

import (
	"context"  // 用于控制并发操作的生命周期，如超时和取消
	"io"       // 用于读取和丢弃 HTTP 响应体
	"net/http" // 用于执行 HTTP 请求
	"openrouter_polling/apimanager"
	"openrouter_polling/config"
	"openrouter_polling/utils"
	"strings" // 用于字符串操作，例如检查错误消息内容
	"time"    // 用于定时任务和超时控制

	"github.com/sirupsen/logrus" // 日志库
)

// 全局变量，将在 main.go 中初始化并注入依赖。
var (
	Log       *logrus.Logger            // 全局日志记录器实例。
	ApiKeyMgr *apimanager.ApiKeyManager // API 密钥管理器实例。
	// HttpClient is not directly needed here as we create a short-lived one for health checks.
	// 健康检查通常使用独立的、具有较短超时的 HTTP 客户端，以避免影响主应用 HttpClient 的配置。
)

// PerformPeriodicHealthChecks 启动一个后台 goroutine，定期对 API 密钥执行健康检查。
// ctx: 一个父上下文，用于在应用程序关闭时取消此后台任务。
func PerformPeriodicHealthChecks(ctx context.Context) {
	// 初始延迟：避免应用刚启动就立即执行（可能密集的）健康检查。
	// 这给应用其他部分（如API服务）一些时间来完全初始化。
	initialDelay := 15 * time.Second // 例如，延迟15秒
	select {
	case <-time.After(initialDelay): // 等待初始延迟结束
		// 延迟结束，继续执行
	case <-ctx.Done(): // 如果在延迟期间应用程序关闭 (父上下文被取消)
		Log.Info("健康检查任务在初始延迟期间被父上下文取消。")
		return // 退出 goroutine
	}

	Log.Info("启动 OpenRouter API 密钥的定期健康检查任务。")
	// 创建一个定时器 (ticker)，根据配置的 HealthCheckInterval 定期触发。
	ticker := time.NewTicker(config.AppSettings.HealthCheckInterval)
	defer ticker.Stop() // 确保在函数退出时停止 ticker，释放相关资源。

	// 主循环，等待 ticker 触发或上下文取消。
	for {
		select {
		case <-ctx.Done(): // 如果父上下文被取消 (例如，应用程序正在关闭)
			Log.Info("健康检查任务因父上下文取消而停止。")
			return // 退出 goroutine

		case <-ticker.C: // 定时器触发，执行一次健康检查周期
			Log.Debug("健康检查: 运行计划中的 API 密钥健康检查周期...")

			// 获取需要检查的密钥快照。
			// ApiKeyMgr.KeysStatusAccessor() 返回一个当前密钥状态的副本列表，
			// 并且内部会调用 checkAndReactivateKeysInternal() 来基于时间自动重新激活冷却完成的密钥。
			// 操作副本可以最小化在（可能较长的）健康检查期间对 ApiKeyManager 主锁的持有。
			keysToCheckSnapshot := ApiKeyMgr.KeysStatusAccessor()

			if len(keysToCheckSnapshot) == 0 {
				Log.Debug("健康检查: 当前没有需要主动检查的密钥（基于快照）。")
				continue // 没有密钥需要检查，等待下一个周期。
			}
			Log.Debugf("健康检查: 快照中包含 %d 个候选密钥，将逐个评估其当前状态。", len(keysToCheckSnapshot))

			// 为本轮健康检查创建一个专用的、短生命周期的 HTTP 客户端。
			// 健康检查请求的超时应独立于主应用 HttpClient 的超时设置。
			healthCheckClient := &http.Client{
				Timeout: 15 * time.Second, // 为单个健康检查请求设置一个合理的短超时（例如15秒）。
			}

			checkedCount := 0
			// 遍历快照中的每个密钥状态。
			for _, ksSnapshot := range keysToCheckSnapshot {
				// 在进行昂贵的网络调用之前，从 ApiKeyManager 获取该密钥的最新状态。
				// 这是因为从获取快照到处理此特定密钥之间，其状态可能已发生变化（例如，已被其他请求使用并激活/禁用）。
				currentKsStatus := ApiKeyMgr.GetKeyStatusByKeyStr(ksSnapshot.Key) // 此方法内部会加锁。
				if currentKsStatus == nil {
					// 密钥可能在获取快照后已被从管理器中移除（例如，通过管理员操作）。
					Log.Debugf("健康检查: 密钥 %s 在快照生成后似乎已从管理器中移除，跳过检查。", utils.SafeSuffix(ksSnapshot.Key))
					continue
				}

				// 确定是否需要对当前密钥进行主动健康检查。
				// 条件：
				// 1. 密钥当前状态为非活动 (IsActive=false)，或之前有失败记录 (FailureCount > 0)。
				// 2. 并且 (密钥现在可用（例如，冷却期已过，IsActive 可能已被自动或手动置为 true），
				//    或其冷却期即将结束)。
				isCandidateForCheck := !currentKsStatus.IsActive || currentKsStatus.FailureCount > 0

				// 检查密钥是否可用或冷却期是否即将结束。
				// currentKsStatus.CanUse() 通常只检查 IsActive。
				// 在此上下文中，我们更关心的是即使 IsActive=false，如果冷却期已过或将过，也应尝试检查。
				canBeUsedOrNearExpiry := !currentKsStatus.IsCurrentlyCoolingDown() // 如果不在冷却中，则认为可以尝试检查。
				if currentKsStatus.IsCurrentlyCoolingDown() {
					// 如果仍在冷却，检查是否即将结束。
					cooldownRemaining := time.Until(*currentKsStatus.CoolDownUntil)
					// “即将结束”的定义：例如，剩余冷却时间小于总冷却时间的1/5，或小于一个固定的短窗口（如2分钟）。
					// 选择一个合理的阈值，避免过于频繁地检查即将解封的密钥。
					nearExpiryThreshold := config.AppSettings.KeyFailureCooldown / 5 // 例如，总冷却的20%
					if nearExpiryThreshold < 1*time.Minute {                         // 最小阈值
						nearExpiryThreshold = 1 * time.Minute
					}
					if cooldownRemaining > 0 && cooldownRemaining < nearExpiryThreshold {
						canBeUsedOrNearExpiry = true
						Log.Debugf("健康检查: 密钥 %s 冷却期即将结束 (剩余: %v)，准备进行检查。",
							utils.SafeSuffix(currentKsStatus.Key), cooldownRemaining.Round(time.Second))
					}
				}

				if !isCandidateForCheck || !canBeUsedOrNearExpiry {
					// 如果不满足上述条件，则跳过对此密钥的主动网络检查。
					// 例如，密钥已激活且无失败，或仍在冷却且未到期。
					// Log.Debugf("健康检查: 密钥 %s (active: %t, fails: %d, cooling: %t, coolUntil: %v) 不符合主动检查条件。",
					//    utils.SafeSuffix(currentKsStatus.Key), currentKsStatus.IsActive, currentKsStatus.FailureCount,
					//    currentKsStatus.IsCurrentlyCoolingDown(), currentKsStatus.CoolDownUntil)
					continue
				}

				Log.Infof("健康检查: 主动检查密钥 %s (当前状态: Active=%t, Failures=%d, CoolingUntil=%v)",
					utils.SafeSuffix(currentKsStatus.Key), currentKsStatus.IsActive, currentKsStatus.FailureCount, currentKsStatus.CoolDownUntil)
				checkedCount++

				// 为本次健康检查的 HTTP 请求创建一个带超时的上下文。
				// 使用 healthCheckClient 的超时作为此上下文的超时。
				hcCtx, hcCancel := context.WithTimeout(ctx, healthCheckClient.Timeout)

				// 使用 OpenRouter 的 /models 端点进行健康检查。
				// 这个端点通常比较轻量，能验证密钥的有效性，且不消耗太多资源。
				req, err := http.NewRequestWithContext(hcCtx, "GET", config.AppSettings.OpenRouterModelsURL, nil)
				if err != nil { // 理论上不太可能发生，因为URL和方法是固定的。
					Log.Errorf("健康检查: 为密钥 %s 创建请求失败: %v。跳过此密钥的检查。", utils.SafeSuffix(currentKsStatus.Key), err)
					hcCancel() // 确保取消上下文
					continue
				}
				req.Header.Set("Authorization", "Bearer "+currentKsStatus.Key) // 使用待检查的密钥

				resp, err := healthCheckClient.Do(req) // 发送健康检查请求
				hcCancel()                             // 请求完成后，立即取消上下文，释放资源。

				if err != nil { // 网络错误或请求超时
					Log.Warnf("健康检查: 密钥 %s 的请求失败: %v。", utils.SafeSuffix(currentKsStatus.Key), err)
					// 如果错误是由于上下文超时 (hcCtx.Err() == context.DeadlineExceeded) 或错误消息中包含 "timeout"，
					// 则认为密钥可能无法在合理时间内响应，可以将其标记为失败。
					if hcCtx.Err() == context.DeadlineExceeded || (err != nil && (strings.Contains(strings.ToLower(err.Error()), "timeout") || strings.Contains(strings.ToLower(err.Error()), "deadline exceeded"))) {
						Log.Warnf("健康检查: 密钥 %s 因超时失败。", utils.SafeSuffix(currentKsStatus.Key))
						ApiKeyMgr.MarkKeyFailure(currentKsStatus.Key) // 超时也算作一次失败，会触发冷却逻辑。
					} else {
						// 对于其他网络错误（如连接被拒、DNS问题），可能不立即标记失败，
						// 因为这可能是暂时的网络问题，而不是密钥本身的问题。
						// 但也可以选择标记失败，这取决于策略的严格程度。
						Log.Errorf("健康检查: 密钥 %s 遇到网络错误: %v。暂不改变其状态，等待下次检查或实际使用。", utils.SafeSuffix(currentKsStatus.Key), err)
					}
					continue // 继续检查下一个密钥
				}

				// 确保响应体被完整读取并关闭，以便连接可以被重用。
				_, _ = io.Copy(io.Discard, resp.Body) // 读取并丢弃响应体内容。
				resp.Body.Close()

				// 根据响应状态码更新密钥状态。
				if resp.StatusCode == http.StatusOK { // 200 OK 表示密钥有效且服务可达。
					Log.Infof("健康检查: 密钥 %s 通过健康检查 (状态 %d)。", utils.SafeSuffix(currentKsStatus.Key), resp.StatusCode)
					ApiKeyMgr.RecordKeySuccess(currentKsStatus.Key) // 标记密钥成功/重新激活。
				} else if resp.StatusCode == http.StatusUnauthorized || // 401 未授权
					resp.StatusCode == http.StatusForbidden || // 403 禁止
					resp.StatusCode == http.StatusTooManyRequests { // 429 请求过多 (速率限制)
					// 这些状态码通常明确表示密钥有问题 (无效、被吊销、达到限额等)。
					Log.Warnf("健康检查: 密钥 %s 验证失败，返回状态 %d。", utils.SafeSuffix(currentKsStatus.Key), resp.StatusCode)
					ApiKeyMgr.MarkKeyFailure(currentKsStatus.Key) // 标记密钥失败。
				} else {
					// 对于其他非200状态码 (例如 5xx 服务器错误)，记录警告但可能不立即标记密钥失败。
					// 因为 /models 端点本身可能遇到临时问题，而不一定代表密钥无效。
					// 密钥的最终状态会在实际API调用失败时更新。
					Log.Warnf("健康检查: 密钥 %s 的请求返回非预期状态 %d。健康检查暂不改变其状态。",
						utils.SafeSuffix(currentKsStatus.Key), resp.StatusCode)
				}
			} // 结束密钥迭代
			if checkedCount > 0 {
				Log.Debugf("健康检查: 本周期主动检查了 %d 个密钥。", checkedCount)
			} else {
				Log.Debug("健康检查: 本周期没有密钥符合主动检查的条件。")
			}
			Log.Debug("健康检查: API 密钥健康检查周期完成。")
		} // 结束 select
	} // 结束 for 循环
}
