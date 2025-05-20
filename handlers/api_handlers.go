// handlers/api_handlers.go
package handlers

import (
	"bufio"                         // 用于带缓冲的读取，提高流式处理效率
	"bytes"                         // 用于字节缓冲操作，例如创建请求体
	"context"                       // 用于管理请求的上下文，例如超时和取消信号
	"encoding/json"                 // 用于JSON的编码和解码
	"fmt"                           // 用于格式化字符串和输出
	"io"                            // 用于IO操作，如ReadAll和EOF
	"net"                           // 用于网络相关的操作，如net.Error和Timeout检查
	"net/http"                      // 用于HTTP客户端和服务器功能
	"openrouter_polling/apimanager" // 项目内的API密钥管理模块
	"openrouter_polling/config"     // 项目配置模块
	"openrouter_polling/models"     // 项目数据模型模块
	"openrouter_polling/utils"      // 项目工具函数模块
	"strconv"                       // 用于字符串和数字转换 (例如，在错误响应中包含状态码)
	"strings"                       // 用于字符串操作
	"sync/atomic"                   // 原子操作，用于并发安全地更新共享状态（如流式响应中的标志）
	"time"                          // 用于时间相关的操作，如超时控制

	"github.com/gin-gonic/gin"   // Gin Web框架
	"github.com/sirupsen/logrus" // Logrus日志库
)

// 全局变量，将在 main.go 中初始化并注入依赖。
// 这种方式简化了在 handler 函数中访问这些共享实例的过程。
var (
	Log          *logrus.Logger            // 全局日志记录器实例。
	ApiKeyMgr    *apimanager.ApiKeyManager // API 密钥管理器实例。
	HttpClient   *http.Client              // 全局 HTTP 客户端，用于向上游 OpenRouter 发出请求。应配置合理的超时和连接池。
	AppStartTime time.Time                 // 应用程序启动时间，可用于计算运行时长等信息。
)

// 超时常量定义，用于更精细地控制流式响应的超时。
const (
	// firstChunkTimeoutDuration: 等待从 OpenRouter 接收到第一个任何类型数据块（包括注释、空行或真实数据）的最大时长。
	// 如果在此时间内未收到任何字节，可能表示连接问题或上游服务无响应。
	firstChunkTimeoutDuration = 15 * time.Second

	// meaningfulDataTimeoutDuration: 从接收到第一个数据块开始，等待接收到包含实际聊天内容（非空delta）的数据块的最大时长。
	// 这有助于检测流已开始但长时间不发送有效内容的情况。
	// 【注意】如果在此期间收到任何非空行（包括注释或心跳），此超时会被重置。
	meaningfulDataTimeoutDuration = 30 * time.Second
)

// ListModelsHandler 处理 `/v1/models` GET 请求。
// 它会向 OpenRouter 的模型列表 API 发出请求，获取模型信息，
// 然后将其转换为 OpenAI 兼容的格式并返回给客户端。
func ListModelsHandler(c *gin.Context) {
	Log.Debug("ListModelsHandler: 收到 /v1/models 请求")
	clientOriginalContext := c.Request.Context() // 获取客户端原始请求的上下文，用于检测客户端是否断开

	// 在开始处理之前，检查客户端是否已断开连接。
	if clientOriginalContext.Err() == context.Canceled {
		Log.Warn("ListModelsHandler: 客户端在处理请求前已断开连接。")
		// 不发送响应，因为客户端已不在。
		return
	}

	// 为向上游 OpenRouter 发出的请求创建一个新的带超时的上下文。
	// 这个超时基于全局配置 `config.AppSettings.RequestTimeout`。
	reqCtx, cancelReqCtx := context.WithTimeout(clientOriginalContext, config.AppSettings.RequestTimeout)
	defer cancelReqCtx() // 确保函数退出时取消此上下文，释放相关资源。

	// 创建到 OpenRouter /models 端点的 HTTP GET 请求。
	req, err := http.NewRequestWithContext(reqCtx, "GET", config.AppSettings.OpenRouterModelsURL, nil)
	if err != nil {
		Log.Errorf("ListModelsHandler: 创建到 OpenRouter /models 的请求失败: %v", err)
		sendErrorResponse(c, http.StatusInternalServerError, "创建上游服务请求失败。", "internal_server_error", false, clientOriginalContext)
		return
	}
	// 通常，/models 端点不需要特殊的 Authorization 头，因为它返回公开信息。
	// 如果 OpenRouter 将来要求，则需要在这里添加。

	// 使用全局 HttpClient 执行 HTTP 请求。
	resp, err := HttpClient.Do(req)
	if err != nil {
		errMsg := fmt.Sprintf("请求 OpenRouter /models 失败: %v", err)
		Log.Error(errMsg)
		statusToSend := http.StatusBadGateway // 默认上游网关错误
		errType := "upstream_api_error"
		if reqCtx.Err() == context.DeadlineExceeded { // 检查是否是请求超时
			errMsg = "请求上游模型列表服务超时。"
			statusToSend = http.StatusGatewayTimeout
			errType = "upstream_timeout_error"
		} else if clientOriginalContext.Err() == context.Canceled { // 检查客户端是否在此期间断开
			Log.Warn("ListModelsHandler: 客户端在请求 OpenRouter /models 期间断开连接。")
			return // 客户端断开，不再发送响应
		}
		sendErrorResponse(c, statusToSend, errMsg, errType, false, clientOriginalContext)
		return
	}
	defer resp.Body.Close() // 确保响应体在函数结束时关闭。

	// 检查 OpenRouter API 的响应状态码。
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body) // 尝试读取错误响应体以获取更多信息。
		Log.Errorf("ListModelsHandler: OpenRouter /models API 返回非 200 状态: %d. Body: %s", resp.StatusCode, string(bodyBytes))
		sendErrorResponse(c, resp.StatusCode, // 将上游的状态码透传或映射
			fmt.Sprintf("上游模型列表服务错误。状态: %d, 详情: %s", resp.StatusCode, string(bodyBytes)),
			"upstream_api_error", false, clientOriginalContext)
		return
	}

	// 解码 OpenRouter 的 JSON 响应到 models.OpenRouterModelsResponse 结构体。
	var openRouterResp models.OpenRouterModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&openRouterResp); err != nil {
		Log.Errorf("ListModelsHandler: 解码 OpenRouter /models 响应失败: %v", err)
		sendErrorResponse(c, http.StatusInternalServerError, "解析上游模型列表数据失败。", "data_parsing_error", false, clientOriginalContext)
		return
	}

	// 将 OpenRouter 的模型数据转换为 OpenAI 兼容的格式 (models.ModelData)。
	var resultData []models.ModelData
	currentTime := time.Now().Unix() // 获取当前 Unix 时间戳，用于填充 'Created' 字段。

	for _, orModel := range openRouterResp.Data {
		ownedBy := "openrouter" // 默认所有者
		// 尝试从模型ID (例如 "openai/gpt-3.5-turbo") 中提取第一部分作为所有者。
		if parts := strings.SplitN(orModel.ID, "/", 2); len(parts) > 1 && parts[0] != "" {
			ownedBy = parts[0]
		}

		// 为每个模型创建默认的权限信息。
		permissionID := fmt.Sprintf("modelperm-%s-%d", strings.ReplaceAll(orModel.ID, "/", "-"), currentTime) // 创建唯一权限ID
		permission := models.ModelPermission{
			ID: permissionID, Object: "model_permission", Created: currentTime,
			AllowCreateEngine: false, AllowSampling: true, AllowLogprobs: true,
			AllowSearchIndices: false, AllowView: true, AllowFineTuning: false,
			Organization: "*", IsBlocking: false, // 开放给所有组织，非阻塞。
		}

		// 创建 OpenAI 格式的模型条目。
		modelEntry := models.ModelData{
			ID: orModel.ID, Object: "model", Created: currentTime, OwnedBy: ownedBy,
			Permissions: []models.ModelPermission{permission}, // 注意 OpenAI schema 是 "permission"，但内容是数组
			Root:        orModel.ID, Parent: nil,              // Root 指向自身，Parent 为 nil (除非有明确的父子关系)。
		}
		resultData = append(resultData, modelEntry)
	}

	Log.Infof("ListModelsHandler: 成功从 OpenRouter 获取并转换了 %d 个模型。", len(resultData))

	// 在发送最终响应前，再次检查客户端是否已断开连接。
	if clientOriginalContext.Err() == context.Canceled {
		Log.Warn("ListModelsHandler: 客户端在准备好响应数据后已断开连接。")
		return
	}

	// 发送转换后的模型列表给客户端。
	c.JSON(http.StatusOK, models.ListModelsResponse{
		Object: "list", // 符合 OpenAI 规范
		Data:   resultData,
	})
}

// ChatCompletionsHandler 处理 `/v1/chat/completions` POST 请求的入口函数。
// 它负责解析请求，确定是否为流式响应，然后调用核心处理逻辑。
func ChatCompletionsHandler(c *gin.Context) {
	clientOriginalContext := c.Request.Context() // 获取客户端原始请求的上下文

	// 请求开始前检查客户端连接状态
	if clientOriginalContext.Err() == context.Canceled {
		Log.Warn("ChatCompletionsHandler: 客户端在处理请求前已断开连接。")
		return
	}

	var requestData models.ChatCompletionRequest
	// 解析请求体 JSON 到 requestData 结构体。
	if err := c.ShouldBindJSON(&requestData); err != nil {
		Log.Warnf("ChatCompletionsHandler: 无效的请求体: %v", err)
		sendErrorResponse(c, http.StatusBadRequest, "请求体解析失败: "+err.Error(), "invalid_request_error", false, clientOriginalContext)
		return
	}

	// 如果请求中未指定模型，使用配置文件中的默认模型。
	if requestData.Model == "" {
		requestData.Model = config.AppSettings.DefaultModel
		Log.Debugf("ChatCompletionsHandler: 请求未指定模型，使用默认模型: %s", requestData.Model)
	}

	// 判断客户端是否期望流式响应。
	// OpenAI 规范：如果 `stream` 字段未提供，默认为 `false`。
	isStreamForClientResponse := false // 默认非流式
	if requestData.Stream != nil {
		isStreamForClientResponse = *requestData.Stream // 使用客户端指定的值
	} else {
		// 如果客户端未指定 stream，我们将其明确设置为 false 以便后续统一处理。
		defaultValueForPayload := false
		requestData.Stream = &defaultValueForPayload
	}

	Log.Infof("收到聊天请求: 模型=%s, 客户端期望流式响应=%t, 用户标识=%s, 客户端IP=%s",
		requestData.Model, isStreamForClientResponse, utils.DerefString(requestData.User, "N/A"), c.ClientIP())

	// 如果是流式响应，设置相应的 HTTP 头部以支持 Server-Sent Events (SSE)。
	if isStreamForClientResponse {
		c.Writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		c.Writer.Header().Set("Cache-Control", "no-cache") // 禁止缓存
		c.Writer.Header().Set("Connection", "keep-alive")  // 保持连接活动
		c.Writer.Header().Set("X-Accel-Buffering", "no")   // 建议：禁用 nginx 等反向代理的缓冲
		c.Writer.Flush()                                   // 确保头部立即发送给客户端
	}

	// 调用核心处理逻辑函数。
	generateChatResponse(c, requestData, isStreamForClientResponse)
}

// generateChatResponse 是实际处理聊天请求的核心逻辑。
// 它管理 API 密钥的选择、请求重试，并根据需要处理流式或非流式响应。
// c: Gin 上下文。
// requestData: 解析后的客户端请求数据。
// isStreamForClientResponse: 客户端是否期望流式响应。
func generateChatResponse(c *gin.Context, requestData models.ChatCompletionRequest, isStreamForClientResponse bool) {
	// 确保发送给 OpenRouter 的 payload 中的 `stream` 字段与客户端的期望一致。
	// 这是因为 OpenRouter 自身也支持流式和非流式。
	if requestData.Stream == nil || *requestData.Stream != isStreamForClientResponse {
		val := isStreamForClientResponse // 创建一个布尔值副本
		requestData.Stream = &val        // 将指针指向此副本
		Log.Debugf("generateChatResponse: 修正发送给 OpenRouter 的 payload 中 stream 字段为: %t", isStreamForClientResponse)
	}

	// 将请求数据序列化为 JSON 字节流，准备发送给 OpenRouter。
	payloadBytes, err := json.Marshal(requestData)
	if err != nil {
		Log.Errorf("generateChatResponse: 序列化请求数据失败: %v", err)
		sendErrorResponse(c, http.StatusInternalServerError, "内部服务器错误：序列化请求失败。", "internal_server_error", isStreamForClientResponse, c.Request.Context())
		return
	}

	retriesLeft := config.AppSettings.RetryWithNewKeyCount // 从配置获取初始重试次数
	var lastExceptionDetail = "在多次尝试使用不同密钥后未能成功处理请求。"      // 默认的最终错误信息
	var lastStatusCode = http.StatusServiceUnavailable     // 默认的最终错误状态码
	var lastErrorType = "api_error"                        // 默认的最终错误类型
	activeRequestKeysTried := make(map[string]bool)        // 记录在本次 `generateChatResponse` 调用中已尝试过的密钥，避免对同一客户端请求用同一坏密钥反复重试。
	clientOriginalContext := c.Request.Context()           // 客户端原始请求的上下文

	// 主重试循环：只要还有重试次数，就继续尝试。
	for retriesLeft >= 0 {
		// 在每次尝试前检查客户端是否已断开连接。
		if clientOriginalContext.Err() == context.Canceled {
			Log.Warnf("generateChatResponse: 客户端在尝试获取新密钥前已断开连接 (重试循环 %d)。请求终止。", config.AppSettings.RetryWithNewKeyCount-retriesLeft)
			return // 客户端已断开，无需继续。
		}

		// 从 ApiKeyManager 获取下一个可用的 API 密钥。
		currentAPIKeyStatus := ApiKeyMgr.GetNextAPIKey()
		if currentAPIKeyStatus == nil {
			Log.Error("generateChatResponse: 管理器没有可用的 API 密钥用于新的尝试。")
			lastExceptionDetail = "所有 API 密钥当前都不可用或处于冷却中。"
			lastStatusCode = http.StatusServiceUnavailable
			lastErrorType = "no_available_keys_error"
			break // 没有可用密钥，跳出重试循环。
		}
		currentOpenRouterKey := currentAPIKeyStatus.Key // 获取密钥字符串

		// 如果当前轮到的密钥已在此请求中尝试过，并且还有其他未尝试的密钥，则尝试获取一个不同的新密钥。
		// 这可以避免在一次用户请求中因密钥选择策略（如轮询）而重复使用一个已知对此请求无效的密钥。
		if _, tried := activeRequestKeysTried[currentOpenRouterKey]; tried && len(activeRequestKeysTried) < ApiKeyMgr.GetTotalKeysCount() {
			Log.Debugf("generateChatResponse: 密钥 %s 在此请求中已尝试过，尝试获取下一个不同的密钥。", utils.SafeSuffix(currentOpenRouterKey))
			foundNewUntriedKey := false
			for i := 0; i < ApiKeyMgr.GetTotalKeysCount(); i++ { // 最多轮询所有密钥一次以查找新密钥
				nextKeyStatusCandidate := ApiKeyMgr.GetNextAPIKey() // 获取下一个（可能还是同一个，如果只有一个可用）
				if nextKeyStatusCandidate == nil {                  // 不太可能发生，但作为保护
					break
				}
				if _, alreadyTried := activeRequestKeysTried[nextKeyStatusCandidate.Key]; !alreadyTried {
					currentAPIKeyStatus = nextKeyStatusCandidate // 找到新的未尝试密钥
					currentOpenRouterKey = currentAPIKeyStatus.Key
					foundNewUntriedKey = true
					Log.Debugf("generateChatResponse: 切换到新的未尝试密钥 %s 用于本次请求。", utils.SafeSuffix(currentOpenRouterKey))
					break
				}
			}
			if !foundNewUntriedKey {
				Log.Warnf("generateChatResponse: 无法为此请求找到新的、未尝试过的可用密钥。当前已尝试 %d 个。将继续使用轮到的密钥 %s。", len(activeRequestKeysTried), utils.SafeSuffix(currentOpenRouterKey))
			}
		}
		activeRequestKeysTried[currentOpenRouterKey] = true // 标记此密钥已被用于当前 `generateChatResponse` 调用。

		// 为本次向上游 API 的尝试创建一个带超时的上下文。
		// 此超时基于全局配置 `config.AppSettings.RequestTimeout`。
		attemptCtx, cancelAttemptCtx := context.WithTimeout(clientOriginalContext, config.AppSettings.RequestTimeout)

		Log.Infof("generateChatResponse: 尝试使用密钥 %s 向 OpenRouter 发起请求 (URL: %s, 剩余重试次数: %d)",
			utils.SafeSuffix(currentOpenRouterKey), config.AppSettings.OpenRouterAPIURL, retriesLeft)

		// 调用封装的单次请求尝试逻辑。
		// attemptOpenRouterRequest 返回:
		//   success (bool): 本次尝试是否成功并将响应完整发送给客户端。
		//   retryNeeded (bool): 如果失败，是否应该用新密钥重试当前客户端请求。
		//   statusCodeForError (int): 如果失败，记录的HTTP状态码。
		//   errorDetailForError (string): 如果失败，记录的错误详情。
		//   errorTypeForError (string): 如果失败，记录的错误类型。
		success, retryNeeded, statusCode, errDetail, errType := attemptOpenRouterRequest(
			attemptCtx, c, currentAPIKeyStatus, payloadBytes, isStreamForClientResponse,
			clientOriginalContext,
		)

		cancelAttemptCtx() // 单次尝试结束（无论成功失败），取消其上下文以释放资源。

		if success {
			Log.Infof("generateChatResponse: 请求使用密钥 %s 成功处理并完成。", utils.SafeSuffix(currentOpenRouterKey))
			return // 请求成功处理，整个 generateChatResponse 结束。
		}

		// 如果尝试失败：
		lastStatusCode = statusCode
		lastExceptionDetail = errDetail
		lastErrorType = errType

		if !retryNeeded { // 如果错误类型指示不应重试 (例如400 Bad Request非密钥问题，或流已部分发送后中断)
			Log.Warnf("generateChatResponse: 发生不可重试的错误 (密钥 %s, 状态码 %d: %s)。终止对此客户端请求的重试。", utils.SafeSuffix(currentOpenRouterKey), statusCode, errDetail)
			break // 退出主重试循环。最终错误将在循环后发送。
		}

		// 如果需要重试：
		// ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey) // MarkKeyFailure 已在 attemptOpenRouterRequest 中根据情况调用
		retriesLeft--
		if retriesLeft < 0 { // 所有重试已用尽
			Log.Errorf("generateChatResponse: 所有重试已用尽。最后失败于密钥 %s。最终错误: %s (状态码 %d)", utils.SafeSuffix(currentOpenRouterKey), lastExceptionDetail, lastStatusCode)
			break // 退出主重试循环。最终错误将在循环后发送。
		}

		Log.Infof("generateChatResponse: 由于错误/超时 (密钥 %s)，将使用新密钥重试 (还剩 %d 次)。错误: %s", utils.SafeSuffix(currentOpenRouterKey), retriesLeft, lastExceptionDetail)
		// 可选：在重试前稍作等待，以避免快速耗尽所有密钥或对上游服务造成冲击。
		time.Sleep(250 * time.Millisecond) // 例如，等待250毫秒。
	} // 结束主重试循环

	// 如果循环结束（所有重试用尽或因不可重试错误跳出），并且客户端未断开连接，则发送最终错误响应。
	if clientOriginalContext.Err() != context.Canceled {
		Log.Errorf("generateChatResponse: 请求最终失败。最后错误: %s (状态码: %d, 类型: %s)", lastExceptionDetail, lastStatusCode, lastErrorType)
		sendErrorResponse(c, lastStatusCode, lastExceptionDetail, lastErrorType, isStreamForClientResponse, clientOriginalContext)
	} else {
		Log.Warnf("generateChatResponse: 请求最终失败，但客户端已断开。不发送最终错误。最后错误: %s (状态码: %d)", lastExceptionDetail, lastStatusCode)
	}
}

// attemptOpenRouterRequest 封装了向 OpenRouter 发起单次 API 请求并处理其响应的逻辑。
// attemptCtx: 本次特定尝试的上下文 (带超时)。
// c: Gin 上下文，用于向客户端发送响应。
// apiKeyStatus: 当前尝试使用的 ApiKeyStatus 对象。
// payloadBytes: 已序列化为 JSON 的请求体。
// isStreamForClientResponse: 客户端是否期望流式响应。
// clientOriginalContext: 客户端原始请求的上下文，用于检测客户端是否已断开。
// 返回:
//
//	success (bool): 本次尝试是否成功并将响应完整或部分（对于流）发送给了客户端。
//	retryNeeded (bool): 如果失败或部分成功后中断，是否指示上层逻辑用新密钥重试。
//	statusCodeForError (int): 如果发生错误，相关的 HTTP 状态码。
//	errorDetail (string): 如果发生错误，错误详情。
//	errorType (string): 如果发生错误，错误类型。
func attemptOpenRouterRequest(
	attemptCtx context.Context,
	c *gin.Context,
	apiKeyStatus *apimanager.ApiKeyStatus,
	payloadBytes []byte,
	isStreamForClientResponse bool,
	clientOriginalContext context.Context,
) (success bool, retryNeeded bool, statusCodeForError int, errorDetail string, errorType string) {
	currentOpenRouterKey := apiKeyStatus.Key // 获取密钥字符串

	// 创建到 OpenRouter 的 POST 请求。
	req, err := http.NewRequestWithContext(attemptCtx, "POST", config.AppSettings.OpenRouterAPIURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		Log.Errorf("attemptOpenRouterRequest: 创建到 OpenRouter 的请求失败: %v (密钥: %s)", err, utils.SafeSuffix(currentOpenRouterKey))
		// 这种错误通常是内部问题，不一定与密钥有关，但为了安全，可以视为可重试。
		return false, true, http.StatusInternalServerError, "创建上游 API 请求失败。", "internal_server_error"
	}

	// 设置必要的 HTTP 请求头。
	req.Header.Set("Authorization", "Bearer "+currentOpenRouterKey)
	req.Header.Set("Content-Type", "application/json")
	if config.AppSettings.HTTPReferer != "" {
		req.Header.Set("HTTP-Referer", config.AppSettings.HTTPReferer)
	}
	if config.AppSettings.XTitle != "" {
		req.Header.Set("X-Title", config.AppSettings.XTitle)
	}
	// 可以添加其他自定义头部，例如追踪ID等。

	// 使用全局 HttpClient 执行 HTTP 请求。
	resp, err := HttpClient.Do(req)
	if err != nil {
		// 处理 HttpClient.Do 返回的错误 (例如网络错误、上下文超时/取消等)。
		errMsg := fmt.Sprintf("请求 OpenRouter 时出错 (密钥: %s): %v", utils.SafeSuffix(currentOpenRouterKey), err)
		Log.Error(errMsg) // 记录详细错误

		// 根据错误类型决定是否重试和返回的状态码/信息。
		if attemptCtx.Err() == context.DeadlineExceeded { // 单次尝试超时
			ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey) // 超时通常与密钥或其承载的服务有关，标记失败。
			return false, true, http.StatusGatewayTimeout, fmt.Sprintf("请求上游 API 超时 (密钥: %s)。", utils.SafeSuffix(currentOpenRouterKey)), "upstream_timeout_error"
		}
		if attemptCtx.Err() == context.Canceled {
			if clientOriginalContext.Err() == context.Canceled { // 检查是否是原始客户端请求取消导致的
				Log.Warnf("attemptOpenRouterRequest: 客户端上下文在请求 OpenRouter 期间被取消 (密钥: %s). 客户端可能已断开。终止。", utils.SafeSuffix(currentOpenRouterKey))
				return false, false, http.StatusServiceUnavailable, "客户端已断开连接。", "client_disconnected_error" // 不重试，因为客户端已离开。
			}
			// 可能是内部超时或其他原因导致的 attemptCtx 取消。
			ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey) // 假设与密钥相关
			return false, true, http.StatusServiceUnavailable, fmt.Sprintf("上游 API 请求被内部取消 (密钥: %s)。", utils.SafeSuffix(currentOpenRouterKey)), "request_canceled_error"
		}
		// 其他网络错误 (例如 DNS 解析失败、连接被拒绝等)。
		ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey) // 标记失败，因为无法连接到服务。
		return false, true, http.StatusBadGateway, fmt.Sprintf("上游 API 网络错误 (密钥: %s): %v", utils.SafeSuffix(currentOpenRouterKey), err), "network_error"
	}
	defer resp.Body.Close() // 确保响应体在函数结束时关闭。

	// --- 处理 OpenRouter 的响应 ---
	if resp.StatusCode == http.StatusOK { // HTTP 200 OK
		// 至少 API 调用是通的，并且服务器返回了成功状态。
		// 对于流式响应，成功与否还取决于流是否正确完成。
		// 对于非流式，200 OK 基本意味着成功。
		// ApiKeyMgr.RecordKeySuccess(currentOpenRouterKey) // RecordKeySuccess 在流/非流成功后或错误处理中更精确地调用

		if !isStreamForClientResponse {
			// --- 处理非流式响应 ---
			bodyBytes, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				Log.Errorf("attemptOpenRouterRequest: 读取 OpenRouter 非流式响应体失败: %v (密钥: %s)", readErr, utils.SafeSuffix(currentOpenRouterKey))
				// 这种错误通常是服务端问题或网络中断。
				// 不立即标记密钥失败，但也不认为此次请求成功。让上层决定是否用新key重试。
				return false, true, http.StatusInternalServerError, "读取上游非流式响应失败。", "response_read_error"
			}

			// 在发送响应前，检查客户端是否已断开。
			if clientOriginalContext.Err() == context.Canceled {
				Log.Warnf("attemptOpenRouterRequest: 成功获取非流式响应，但客户端已断开 (密钥: %s)。", utils.SafeSuffix(currentOpenRouterKey))
				// 即使客户端断开，也认为对 OpenRouter 的调用是成功的，记录密钥成功。
				ApiKeyMgr.RecordKeySuccess(currentOpenRouterKey)
				return true, false, http.StatusOK, "", "" // 标记为成功获取，不需重试（因为客户端没了）。
			}

			// 将响应体直接透传给客户端。
			c.Data(http.StatusOK, resp.Header.Get("Content-Type"), bodyBytes) // 使用上游的 Content-Type
			Log.Infof("attemptOpenRouterRequest: 成功发送非流式响应 (密钥: %s, 大小: %d bytes)。", utils.SafeSuffix(currentOpenRouterKey), len(bodyBytes))
			ApiKeyMgr.RecordKeySuccess(currentOpenRouterKey) // 记录密钥成功
			return true, false, http.StatusOK, "", ""        // 成功处理，无需重试。
		}

		// --- 处理流式响应 ---
		Log.Infof("attemptOpenRouterRequest: 开始从 OpenRouter 流式传输数据 (密钥: %s)", utils.SafeSuffix(currentOpenRouterKey))
		// processStreamingResponse 会处理流的读取、超时、错误，并将数据转发给客户端。
		// 它也会在适当的时候调用 ApiKeyMgr.RecordKeySuccess 或决定是否需要重试。
		streamSuccess, streamRetryNeeded, streamErrStatusCode, streamErrDetail, streamErrType := processStreamingResponse(
			attemptCtx, c, resp, apiKeyStatus, clientOriginalContext,
		)

		if !streamSuccess { // 如果流处理不完全成功
			// streamRetryNeeded 会告诉我们是否应该由上层用新密钥重试
			// streamErrStatusCode, streamErrDetail, streamErrType 提供了失败信息
			Log.Warnf("attemptOpenRouterRequest: 流处理未完全成功 (密钥 %s): %s. 重试需求: %t", utils.SafeSuffix(currentOpenRouterKey), streamErrDetail, streamRetryNeeded)
			// MarkKeyFailure 通常已在 processStreamingResponse 中处理
			return false, streamRetryNeeded, streamErrStatusCode, streamErrDetail, streamErrType
		}

		// 流处理成功完成（可能包括客户端中途断开，此时 streamSuccess 仍为 true，但 retryNeeded 为 false）
		if clientOriginalContext.Err() == context.Canceled {
			Log.Warnf("attemptOpenRouterRequest: 流处理完成/中止，因客户端已断开 (密钥 %s)。", utils.SafeSuffix(currentOpenRouterKey))
		} else {
			Log.Infof("attemptOpenRouterRequest: 流式响应处理完成 (密钥 %s)。", utils.SafeSuffix(currentOpenRouterKey))
		}
		// ApiKeyMgr.RecordKeySuccess(currentOpenRouterKey) 已在 processStreamingResponse 内部成功时调用
		return true, false, http.StatusOK, "", "" // 流处理完成或因不可重试原因结束。

	} else {
		// --- OpenRouter 返回非 200 OK 状态码 ---
		// 调用 handleOpenRouterErrorResponse 处理错误，它会决定是否标记密钥失败和是否需要重试。
		_, shouldRetry, errCode, errStr, errTypeStr := handleOpenRouterErrorResponse(resp, currentOpenRouterKey, clientOriginalContext)
		if shouldRetry { // 如果错误类型指示密钥可能有问题
			ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey)
		}
		return false, shouldRetry, errCode, errStr, errTypeStr
	}
}

// processStreamingResponse 负责处理来自 OpenRouter 的流式响应。
// 它会逐行读取数据，转发给客户端，并处理特定的超时和错误情况。
// attemptCtx: 本次 API 调用的上下文，用于检测整体调用是否超时或取消。
// c: Gin 上下文，用于向客户端写入数据。
// resp: 来自 OpenRouter 的 HTTP 响应对象。
// apiKeyStatus: 当前使用的 ApiKeyStatus 对象。
// clientOriginalContext: 客户端原始请求的上下文，用于检测客户端是否已断开。
// 返回:
//
//	streamSuccess (bool): 流是否被认为是成功处理（可能部分成功后客户端断开）。
//	streamRetryNeeded (bool): 如果流处理中发生错误，是否应尝试用新密钥重试。
//	statusCodeForError (int): 如果发生错误，相关的 HTTP 状态码。
//	errorDetail (string): 如果发生错误，错误详情。
//	errorType (string): 如果发生错误，错误类型。
func processStreamingResponse(
	attemptCtx context.Context,
	c *gin.Context,
	resp *http.Response,
	apiKeyStatus *apimanager.ApiKeyStatus,
	clientOriginalContext context.Context,
) (streamSuccess bool, streamRetryNeeded bool, statusCodeForError int, errorDetail string, errorType string) {
	currentOpenRouterKey := apiKeyStatus.Key
	reader := bufio.NewReader(resp.Body) // 带缓冲的读取器，提高效率。
	processedDone := false               // 标记是否已处理过 SSE 的 "[DONE]" 信号。
	var firstChunkReceivedTime time.Time // 记录收到第一个任何类型数据块的时间。
	var receivedMeaningfulData int32     // 原子标志 (0=false, 1=true)，标记是否已收到包含实际内容的聊天数据。
	var errRead error                    // 保存从 reader.ReadString 返回的错误。

	// --- 超时控制 ---
	// firstAnyDataTimer: 等待从 OpenRouter 返回的第一个字节（可以是注释、空行或数据）。
	firstAnyDataTimer := time.NewTimer(firstChunkTimeoutDuration)
	defer firstAnyDataTimer.Stop() // 确保计时器在函数退出时停止，释放资源。

	// meaningfulDataTimer: 在收到第一个字节后，等待第一个包含实际内容的聊天数据。
	// 使用 channel 将 timer 的超时事件传递到主 select 循环中。
	var meaningfulDataTimer *time.Timer
	meaningfulDataTimerChan := make(chan bool, 1) // 容量为1的缓冲channel

	// 尝试获取底层的 net.Conn 以设置读取截止时间，用于更精细的 I/O 超时控制。
	var connWithDeadline interface{ SetReadDeadline(time.Time) error }
	if cw, ok := resp.Body.(interface{ SetReadDeadline(time.Time) error }); ok {
		connWithDeadline = cw
		Log.Debugf("processStreamingResponse: 响应体支持 SetReadDeadline (密钥: %s)", utils.SafeSuffix(currentOpenRouterKey))
	} else {
		Log.Debugf("processStreamingResponse: 响应体不支持 SetReadDeadline (密钥: %s)，将依赖外部 Timer 进行超时控制。", utils.SafeSuffix(currentOpenRouterKey))
	}

	// setReadDeadline 辅助函数：如果支持，则设置响应体的读取截止时间。
	setReadDeadlineWrapper := func(duration time.Duration) {
		if connWithDeadline != nil {
			deadline := time.Now().Add(duration)
			if err := connWithDeadline.SetReadDeadline(deadline); err != nil {
				Log.Warnf("processStreamingResponse: 无法为流式响应设置读取截止时间: %v (密钥: %s)", err, utils.SafeSuffix(currentOpenRouterKey))
			}
		}
	}
	// clearReadDeadline 辅助函数：如果支持，则清除响应体的读取截止时间。
	clearReadDeadlineWrapper := func() {
		if connWithDeadline != nil {
			if err := connWithDeadline.SetReadDeadline(time.Time{}); err != nil { // 传入零值时间以清除截止。
				// Log.Warnf("processStreamingResponse: 清除读取截止时间失败: %v", err) // 通常不关键
			}
		}
	}
	setReadDeadlineWrapper(firstChunkTimeoutDuration) // 初始设置等待第一块数据的读取超时。

	// 流式数据读取的主循环。
	for {
		// --- 事件检查 ---
		// 优先检查外部上下文取消和计时器超时事件。
		select {
		case <-attemptCtx.Done(): // 整体API调用尝试的上下文被取消 (可能是总超时或上层逻辑取消)。
			Log.Warnf("processStreamingResponse: 流式读取时，尝试上下文被取消 (密钥 %s): %v", utils.SafeSuffix(currentOpenRouterKey), attemptCtx.Err())
			clearReadDeadlineWrapper()
			if clientOriginalContext.Err() == context.Canceled {
				return true, false, http.StatusServiceUnavailable, "客户端已断开连接。", "client_disconnected_error" // 客户端取消，不重试，但流可能已部分成功。
			}
			// 可能是总请求超时，这种情况下我们认为密钥可能存在问题或响应过慢。
			ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey)
			return false, true, http.StatusGatewayTimeout, fmt.Sprintf("上游流为密钥 %s 中断或超时。", utils.SafeSuffix(currentOpenRouterKey)), "upstream_timeout_error"

		case <-clientOriginalContext.Done(): // 客户端原始请求的上下文被取消 (客户端主动断开)。
			Log.Warnf("processStreamingResponse: 客户端在流式传输期间断开 (select检查) (密钥: %s)。", utils.SafeSuffix(currentOpenRouterKey))
			clearReadDeadlineWrapper()
			// 即使客户端断开，也认为对 OpenRouter 的部分调用（如果已开始）是成功的。
			// 如果在收到任何有意义数据前断开，则密钥状态不更新。
			// 如果已收到，ApiKeyMgr.RecordKeySuccess 会在下面处理。
			return true, false, http.StatusServiceUnavailable, "客户端已断开连接。", "client_disconnected_error" // 客户端断开，不重试。

		case <-firstAnyDataTimer.C: // 等待第一块任何数据的计时器超时。
			if firstChunkReceivedTime.IsZero() { // 确认是 firstAnyDataTimer 触发且确实未收到任何数据。
				Log.Warnf("processStreamingResponse: 等待首块任何数据超时 (>%v)，密钥: %s. 标记失败并重试。", firstChunkTimeoutDuration, utils.SafeSuffix(currentOpenRouterKey))
				clearReadDeadlineWrapper()
				ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey)
				return false, true, http.StatusGatewayTimeout, fmt.Sprintf("等待密钥 %s 初始任何数据超时。", utils.SafeSuffix(currentOpenRouterKey)), "initial_data_timeout_error"
			}
			// 如果已收到数据 (firstChunkReceivedTime 非零)，则此超时无效，继续。

		case <-meaningfulDataTimerChan: // 等待有意义数据的计时器超时 (通过channel异步传递)。
			if atomic.LoadInt32(&receivedMeaningfulData) == 0 { // 检查是否仍未收到有意义数据。
				Log.Warnf("processStreamingResponse: 等待有意义聊天数据超时 (>%v)，密钥: %s. 标记失败并重试。", meaningfulDataTimeoutDuration, utils.SafeSuffix(currentOpenRouterKey))
				clearReadDeadlineWrapper()
				ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey)
				return false, true, http.StatusGatewayTimeout, fmt.Sprintf("等待密钥 %s 有意义聊天数据超时。", utils.SafeSuffix(currentOpenRouterKey)), "meaningful_data_timeout_error"
			}
			// 如果已收到有意义数据，则此超时无效，继续。
		default:
			// 非阻塞检查，如果没有事件发生，则继续执行后续的读取操作。
		}

		// --- 执行实际的读取操作 ---
		var line string
		// 从响应体中读取一行数据 (直到 '\n')。
		// SetReadDeadline (如果支持) 会在这里生效，如果超时会返回 net.Error。
		line, errRead = reader.ReadString('\n')

		// --- 处理首次数据接收逻辑 ---
		if firstChunkReceivedTime.IsZero() && (errRead == nil || (errRead == io.EOF && line != "")) { // 收到第一行数据（或EOF但行非空）
			firstChunkReceivedTime = time.Now() // 记录时间
			Log.Debugf("processStreamingResponse: 收到首块任何数据 (密钥: %s，耗时 %s，原始行: %q)", // 【注意】这里的耗时是相对 firstChunkReceivedTime=Zero 时的，实际应该从请求开始计时
				utils.SafeSuffix(currentOpenRouterKey), time.Since(firstChunkReceivedTime).Round(time.Millisecond), line)

			firstAnyDataTimer.Stop() // 成功收到数据，停止 firstAnyDataTimer。

			// 启动有意义数据超时计时器 (meaningfulDataTimer)。
			if meaningfulDataTimer != nil { // 防御性编程，理论上此时应为 nil。
				meaningfulDataTimer.Stop()
			}
			meaningfulDataTimer = time.NewTimer(meaningfulDataTimeoutDuration)
			// 启动一个 goroutine 监听此计时器，并将超时信号发送到 meaningfulDataTimerChan。
			// 捕获当前的 timer 和 key 副本，避免闭包问题。
			go func(timer *time.Timer, key string, mdtChan chan<- bool, attemptCtxForTimer context.Context) {
				select {
				case <-timer.C: // 定时器触发
					// 在发送信号前再次检查，确保在计时器goroutine执行时确实没收到有意义数据。
					// 这是因为主循环可能在计时器触发和此goroutine响应之间收到了数据。
					if atomic.LoadInt32(&receivedMeaningfulData) == 0 {
						mdtChan <- true // 发送超时信号
					}
				case <-attemptCtxForTimer.Done(): // 如果父上下文取消，也应停止此goroutine。
					// 父上下文取消意味着整个尝试已结束，无需再等待此特定超时。
					if timer != nil {
						timer.Stop()
					} // 安全地停止计时器。
					return
				}
			}(meaningfulDataTimer, currentOpenRouterKey, meaningfulDataTimerChan, attemptCtx)
			Log.Debugf("processStreamingResponse: 已启动 %v 的有意义数据超时计时器 (密钥: %s)", meaningfulDataTimeoutDuration, utils.SafeSuffix(currentOpenRouterKey))
		}

		// 【关键修改】: 如果已收到首块数据，但尚未收到有意义数据，并且当前成功读取到非空行，则重置有意义数据定时器。
		// 这样可以允许“思考中”的数据块（如注释行或空内容的数据块）作为心跳来延长等待有意义内容的时间。
		if !firstChunkReceivedTime.IsZero() && atomic.LoadInt32(&receivedMeaningfulData) == 0 && errRead == nil && len(strings.TrimSpace(line)) > 0 {
			if meaningfulDataTimer != nil {
				Log.Debugf("processStreamingResponse: 收到活动行 (密钥: %s)，重置有意义数据定时器。行: %q", utils.SafeSuffix(currentOpenRouterKey), strings.TrimSpace(line))
				// 停止旧的timer并尝试清空其channel，以防旧的超时信号干扰。
				// Stop 返回 false 表示 timer 已经过期或已经被 Stop。
				if !meaningfulDataTimer.Stop() {
					// 尝试消耗可能已发送的信号，避免主select循环捕获到旧的超时。
					select {
					case <-meaningfulDataTimerChan:
						Log.Debugf("processStreamingResponse: 从 meaningfulDataTimerChan 中消耗了一个旧的超时信号。")
					default:
					}
				}
				meaningfulDataTimer.Reset(meaningfulDataTimeoutDuration)
			}
		}

		// 根据当前流的状态动态调整读取截止时间。
		if atomic.LoadInt32(&receivedMeaningfulData) == 0 && !firstChunkReceivedTime.IsZero() {
			// 如果已收到首块数据但还未收到有意义数据，则继续使用较短的 meaningfulDataTimeoutDuration 作为读取超时。
			setReadDeadlineWrapper(meaningfulDataTimeoutDuration)
		} else if atomic.LoadInt32(&receivedMeaningfulData) == 1 {
			// 如果已收到有意义数据，可以将读取超时设置得更长（例如等于请求总超时），或依赖整体的 attemptCtx 超时。
			// 这里选择清除特定的短时读取超时，主要依赖 attemptCtx。
			clearReadDeadlineWrapper()
		}

		// --- 处理读取到的行数据 ---
		if len(line) > 0 { // 如果读取到非空行
			if Log.GetLevel() >= logrus.DebugLevel { // 避免在高并发下因日志格式化影响性能。
				Log.Debugf("processStreamingResponse: 从 OpenRouter 读取行 (密钥 %s): %q", utils.SafeSuffix(currentOpenRouterKey), line)
			}

			// 尝试将读取到的行数据写入客户端响应。
			if _, errWrite := c.Writer.WriteString(line); errWrite != nil {
				Log.Warnf("processStreamingResponse: 写入流数据到客户端失败: %v (密钥: %s). 客户端可能已断开。", errWrite, utils.SafeSuffix(currentOpenRouterKey))
				clearReadDeadlineWrapper()
				// 客户端断开，不标记密钥失败（因为它可能工作正常），也不重试。
				return true, false, http.StatusServiceUnavailable, "写入客户端失败。", "client_write_error"
			}
			// 如果写入成功，并且 Writer 支持 http.Flusher 接口，则刷新缓冲区。
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush() // 确保数据立即发送给客户端。
			}

			// 检查 SSE 内容以更新状态 (例如，是否收到 "[DONE]" 或有意义数据)。
			trimmedLine := strings.TrimSpace(line)
			if strings.HasPrefix(trimmedLine, models.SSEDataPrefix) { // "data: "
				dataContent := strings.TrimSpace(strings.TrimPrefix(trimmedLine, models.SSEDataPrefix))
				if dataContent == models.SSEDonePayload { // "[DONE]"
					Log.Infof("processStreamingResponse: 收到显式 [DONE] 信号 (密钥: %s)", utils.SafeSuffix(currentOpenRouterKey))
					processedDone = true // 标记已处理 [DONE]。
					errRead = io.EOF     // 将 errRead 设为 io.EOF 以便跳出循环，表示流正常结束。
				} else if atomic.LoadInt32(&receivedMeaningfulData) == 0 { // 仅在还未标记收到有意义数据时检查。
					var chunk models.ChatCompletionChunk
					// 尝试解析数据块，检查是否包含实际内容。
					if errJson := json.Unmarshal([]byte(dataContent), &chunk); errJson == nil {
						// 检查 Delta.Content 是否非空。
						if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != nil && *chunk.Choices[0].Delta.Content != "" {
							Log.Infof("processStreamingResponse: 收到首块有意义聊天数据 (密钥: %s): %q", utils.SafeSuffix(currentOpenRouterKey), *chunk.Choices[0].Delta.Content)
							atomic.StoreInt32(&receivedMeaningfulData, 1) // 标记已收到有意义数据。
							if meaningfulDataTimer != nil {
								meaningfulDataTimer.Stop() // 停止 meaningfulDataTimer。
							}
							clearReadDeadlineWrapper()                       // 清除特定的短时读取超时。
							ApiKeyMgr.RecordKeySuccess(currentOpenRouterKey) // 收到有意义数据，认为密钥是好的
						} else {
							Log.Debugf("processStreamingResponse: 收到数据块，但内容为空或非聊天内容 (密钥 %s): %q", utils.SafeSuffix(currentOpenRouterKey), dataContent)
						}
					} else {
						// 如果 JSON 解析失败，可能不是标准聊天块，但仍是数据。
						Log.Warnf("processStreamingResponse: 无法解析收到的 data 块 JSON (密钥 %s, 内容可能非标准聊天块，忽略检查有意义内容): %v, data: %q", utils.SafeSuffix(currentOpenRouterKey), errJson, dataContent)
					}
				}
			} else if strings.HasPrefix(trimmedLine, ":") { // SSE 注释/心跳行
				Log.Debugf("processStreamingResponse: 收到注释/心跳行 (密钥 %s): %q", utils.SafeSuffix(currentOpenRouterKey), trimmedLine)
				// 注释行也算是服务器有响应。上面的【关键修改】部分会处理重置 meaningfulDataTimer。
			}
		} // 结束 if len(line) > 0

		// --- 处理读取错误 (EOF, timeout, etc.) ---
		if errRead != nil {
			// 在处理任何读取错误之前，停止所有活跃的特定于流的计时器。
			// firstAnyDataTimer 已经通过 defer Stop() 处理。
			if meaningfulDataTimer != nil {
				meaningfulDataTimer.Stop()
			}
			clearReadDeadlineWrapper() // 清除可能存在的读取截止时间。

			// 如果错误是网络超时 (由 SetReadDeadline 触发)
			if netErr, ok := errRead.(net.Error); ok && netErr.Timeout() {
				if firstChunkReceivedTime.IsZero() {
					Log.Warnf("processStreamingResponse: 等待首块数据超时 (net.Error, ReadDeadline)，密钥: %s. 标记失败并重试。", utils.SafeSuffix(currentOpenRouterKey))
					ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey)
					return false, true, http.StatusGatewayTimeout, fmt.Sprintf("等待密钥 %s 初始数据超时 (net.Error)。", utils.SafeSuffix(currentOpenRouterKey)), "initial_data_timeout_error"
				} else if atomic.LoadInt32(&receivedMeaningfulData) == 0 {
					Log.Warnf("processStreamingResponse: 等待有意义聊天数据超时 (net.Error, ReadDeadline)，密钥: %s. 标记失败并重试。", utils.SafeSuffix(currentOpenRouterKey))
					ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey)
					return false, true, http.StatusGatewayTimeout, fmt.Sprintf("等待密钥 %s 有意义聊天数据超时 (net.Error)。", utils.SafeSuffix(currentOpenRouterKey)), "meaningful_data_timeout_error"
				} else {
					// 已经收到有意义数据后发生的读取超时，可能是网络问题或服务器提前关闭连接。
					Log.Warnf("processStreamingResponse: 读取后续数据块时网络超时 (net.Error, ReadDeadline): %v, 密钥: %s. 流已部分发送，不重试。", errRead, utils.SafeSuffix(currentOpenRouterKey))
					// 已经给客户端发送了部分数据，通常不应在此请求内静默重试。让客户端感知到流中断。
					// 密钥可能没问题，也可能是暂时性网络故障，不立即标记失败。
					return true, false, http.StatusGatewayTimeout, fmt.Sprintf("密钥 %s 流传输中网络超时。", utils.SafeSuffix(currentOpenRouterKey)), "subsequent_data_timeout_error"
				}
			}

			// 如果错误是 io.EOF (流正常结束或已收到 "[DONE]")
			if errRead == io.EOF {
				Log.Infof("processStreamingResponse: OpenRouter 流结束 (EOF 或 [DONE] 已处理) (密钥: %s)", utils.SafeSuffix(currentOpenRouterKey))
				if !processedDone && atomic.LoadInt32(&receivedMeaningfulData) == 1 {
					// 如果已收到有意义数据，但流结束时没有显式收到 "[DONE]" 信号
					// （例如，上游直接关闭连接），则我们手动为客户端补发一个 "[DONE]"。
					Log.Debugf("processStreamingResponse: 流结束但未收到显式 [DONE]，且已收到有意义数据，手动发送 [DONE] (密钥: %s)", utils.SafeSuffix(currentOpenRouterKey))
					if clientOriginalContext.Err() != context.Canceled { // 确保客户端还连接着
						if _, errWrite := fmt.Fprintf(c.Writer, "%s%s\n\n", models.SSEDataPrefix, models.SSEDonePayload); errWrite != nil {
							Log.Warnf("processStreamingResponse: 发送最终 [DONE] 失败: %v (密钥: %s)", errWrite, utils.SafeSuffix(currentOpenRouterKey))
						} else if flusher, ok := c.Writer.(http.Flusher); ok {
							flusher.Flush()
						}
					}
				} else if !processedDone && atomic.LoadInt32(&receivedMeaningfulData) == 0 && !firstChunkReceivedTime.IsZero() {
					// 流结束了，但连有意义的数据都没收到 (并且已收到过首块数据，排除了首块超时的情况)。
					// 这可能表示密钥有效但模型无法生成内容，或者上游服务有问题。
					Log.Warnf("processStreamingResponse: OpenRouter 流在收到有意义数据前意外结束 (EOF)，密钥 %s。标记失败并重试。", utils.SafeSuffix(currentOpenRouterKey))
					ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey)
					return false, true, http.StatusBadGateway, fmt.Sprintf("密钥 %s 的流在发送有意义数据前意外结束。", utils.SafeSuffix(currentOpenRouterKey)), "premature_eof_error"
				}
				// 如果 processedDone 为 true，或未收到有意义数据前EOF (firstChunkReceivedTime is Zero, handled by timer)，则流正常结束。
				// ApiKeyMgr.RecordKeySuccess 应该在收到有意义数据时已被调用。
				return true, false, http.StatusOK, "", "" // 流正常结束（或已处理），不需重试。
			}

			// 其他类型的读取错误。
			// 再次检查父上下文是否已取消（可能在 ReadString 阻塞期间发生）。
			if attemptCtx.Err() != nil { // 涵盖 Canceled 和 DeadlineExceeded
				Log.Errorf("processStreamingResponse: 读取流时发现尝试上下文已取消/超时: %v (读取错误: %v, 密钥: %s).", attemptCtx.Err(), errRead, utils.SafeSuffix(currentOpenRouterKey))
				if clientOriginalContext.Err() == context.Canceled {
					return true, false, http.StatusServiceUnavailable, "客户端已断开。", "client_disconnected_error" // 客户端主动取消，不重试。
				}
				// 可能是整体请求超时或内部取消，标记为可重试，并标记密钥失败。
				ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey)
				return false, true, http.StatusGatewayTimeout, fmt.Sprintf("上游流为密钥 %s 中断或超时 (读取错误: %v)。", utils.SafeSuffix(currentOpenRouterKey), errRead), "upstream_timeout_on_read_error"
			}

			// 对于其他未知或未特定处理的读取错误。
			Log.Errorf("processStreamingResponse: 读取流时意外错误: %v (密钥: %s). 标记失败并重试。", errRead, utils.SafeSuffix(currentOpenRouterKey))
			ApiKeyMgr.MarkKeyFailure(currentOpenRouterKey)
			return false, true, http.StatusInternalServerError, fmt.Sprintf("读取上游流为密钥 %s 时发生错误: %v", utils.SafeSuffix(currentOpenRouterKey), errRead), "stream_read_error"
		} // 结束 if errRead != nil
	} // 结束 for 流式数据读取循环
}

// handleOpenRouterErrorResponse 处理 OpenRouter 返回的非 200 OK HTTP 状态码。
// 它解析错误响应，记录日志，并根据错误类型决定是否应重试以及是否标记密钥失败。
// resp: 来自 OpenRouter 的 HTTP 响应对象。
// currentOpenRouterKey: 当前使用的 API 密钥字符串。
// clientOriginalContext: 客户端原始请求的上下文。
// 返回:
//
//	success (bool): 总是 false，因为这是错误处理。
//	retryNeeded (bool): 是否应该使用新密钥重试当前客户端请求。
//	statusCodeForError (int): 从 OpenRouter 收到的 HTTP 状态码。
//	errorDetail (string): 从 OpenRouter 收到的错误详情。
//	errorType (string): 根据状态码推断的错误类型。
func handleOpenRouterErrorResponse(resp *http.Response, currentOpenRouterKey string, clientOriginalContext context.Context) (success bool, retryNeeded bool, statusCodeForError int, errorDetail string, errorType string) {
	errorContentBytes, _ := io.ReadAll(resp.Body) // 尝试读取错误响应体。
	errorDetailStr := strings.TrimSpace(string(errorContentBytes))
	if errorDetailStr == "" {
		errorDetailStr = fmt.Sprintf("上游服务返回状态码 %d，但响应体为空。", resp.StatusCode)
	}

	Log.Warnf("handleOpenRouterErrorResponse: OpenRouter API 错误 (状态码: %d) 使用密钥 %s: %s",
		resp.StatusCode, utils.SafeSuffix(currentOpenRouterKey), errorDetailStr)

	// 在处理错误前，检查客户端是否已断开连接。
	if clientOriginalContext.Err() == context.Canceled {
		Log.Warnf("handleOpenRouterErrorResponse: 客户端在 OpenRouter 返回错误 (%d) 后已断开。不重试。", resp.StatusCode)
		// 密钥本身可能有问题，但由于客户端已断开，不重试此请求。
		// MarkKeyFailure 将在 attemptOpenRouterRequest 返回后，如果 retryNeeded 为 true 时调用。
		// 这里返回 retryNeeded=true 是为了让上层标记密钥，即使客户端断了。
		return false, true, resp.StatusCode, "客户端已断开连接，但上游返回错误。", "client_disconnected_with_upstream_error"
	}

	shouldRetry := false                                // 默认不重试
	var inferredErrorType string = "upstream_api_error" // 默认错误类型

	// 根据 HTTP 状态码判断是否需要重试以及推断错误类型。
	// ApiKeyMgr.MarkKeyFailure 的调用将由 attemptOpenRouterRequest 根据 shouldRetry 决定。
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden: // 401, 403: 密钥无效、无权限、账户问题。
		shouldRetry = true // 这些错误通常与特定密钥相关，应尝试其他密钥。
		inferredErrorType = "authentication_error"
	case http.StatusTooManyRequests: // 429: 速率限制。
		shouldRetry = true // 密钥可能已达到其速率限制，尝试其他密钥。
		inferredErrorType = "rate_limit_error"
		// 可选：为 429 错误实现更复杂的退避逻辑，例如特定密钥的临时更长冷却。
	case http.StatusBadRequest: // 400: 错误的请求。
		lowerErrorDetail := strings.ToLower(errorDetailStr)
		// 检查错误信息是否明确指示与密钥、配额或账户相关的问题。
		if strings.Contains(lowerErrorDetail, "invalid api key") || strings.Contains(lowerErrorDetail, "quota") ||
			strings.Contains(lowerErrorDetail, "credit") || strings.Contains(lowerErrorDetail, "balance") || strings.Contains(lowerErrorDetail, "funds") ||
			strings.Contains(lowerErrorDetail, "insufficient_quota") {
			shouldRetry = true                  // 与密钥/账户相关的问题，尝试其他密钥。
			inferredErrorType = "billing_error" // 或 "authentication_error"
		} else {
			// 其他类型的 400 错误，很可能是客户端请求参数本身的问题（例如，无效的模型名称、格式错误的 messages）。
			// 这种情况下不应该重试密钥，因为问题不在密钥。直接将错误返回给客户端。
			Log.Warnf("handleOpenRouterErrorResponse: OpenRouter 返回 400 Bad Request (非密钥/配额类): %s。请求参数可能存在问题，不重试。", errorDetailStr)
			shouldRetry = false
			inferredErrorType = "invalid_request_error"
		}
	case http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusBadGateway: // 500, 503, 502: 上游服务器内部问题。
		shouldRetry = true // 上游服务器暂时性问题，可以尝试其他密钥或稍后重试同一密钥（通过冷却和健康检查）。
		inferredErrorType = "upstream_server_error"
		time.Sleep(500 * time.Millisecond) // 上游服务器错误，稍等片刻再用新密钥重试可能有助于缓解。
	default:
		// 对于其他未明确处理的错误码 (例如 404 Not Found, 415 Unsupported Media Type 等)。
		// 通常这些错误与请求本身（例如错误的路径、内容类型）或模型特定问题相关，而不是密钥本身。
		Log.Warnf("handleOpenRouterErrorResponse: OpenRouter 返回未特殊处理的错误码 %d: %s。默认不重试。", resp.StatusCode, errorDetailStr)
		shouldRetry = false             // 默认不重试这些类型的错误。
		inferredErrorType = "api_error" // 通用API错误
	}

	return false, shouldRetry, resp.StatusCode, fmt.Sprintf("上游 API 错误 (状态 %d): %s", resp.StatusCode, errorDetailStr), inferredErrorType
}

// sendErrorResponse 统一向客户端发送错误响应。
// c: Gin 上下文。
// statusCode: 要发送给客户端的 HTTP 状态码。
// message: 人类可读的错误消息。
// errorType: 错误类型字符串 (例如 "api_error", "invalid_request_error")。
// isStream: 当前请求是否为流式请求。如果是，错误将以 SSE 事件形式发送。
// originalCtx: 客户端原始请求的上下文，用于检查客户端是否已断开。
func sendErrorResponse(c *gin.Context, statusCode int, message string, errorType string, isStream bool, originalCtx context.Context) {
	// 如果客户端已断开连接，则不发送任何响应，仅记录日志。
	if originalCtx != nil && originalCtx.Err() == context.Canceled {
		Log.Warnf("sendErrorResponse: 尝试发送错误 '%s' (状态码 %d)，但客户端已断开。不发送。", message, statusCode)
		return
	}

	// 如果响应头已写入 (通常意味着部分数据已发送，或WriteHeader已被调用)。
	if c.Writer.Written() {
		// 对于非流式请求，如果头已写，通常无法再发送标准 JSON 错误体。
		if !isStream {
			Log.Warnf("sendErrorResponse: 尝试发送 JSON 错误 '%s' (状态码 %d)，但响应头已写入。不发送。", message, statusCode)
			return
		}
		// 对于流式请求，即使头已写（通常是 200 OK for SSE），我们仍可以尝试发送错误事件。
		// 检查已发送的状态码，如果不是200，记录一个更详细的警告。
		currentSentStatus := c.Writer.Status()
		if currentSentStatus != http.StatusOK && currentSentStatus != 0 { // 0表示 WriteHeader 未被调用
			Log.Warnf("sendErrorResponse: 尝试发送 SSE 错误事件 '%s'，但响应头已写入且状态非200/0 (%d)。仍将尝试发送错误事件。", message, currentSentStatus)
		} else if currentSentStatus == http.StatusOK {
			Log.Debugf("sendErrorResponse: SSE 流已开始 (200 OK)，现在尝试发送错误事件: %s", message)
		}
	}

	// 构建符合 OpenAI 风格的错误响应体。
	errorPayload := models.ErrorResponse{
		Error: models.ErrorDetail{
			Message: message,
			Type:    errorType,                // 使用传入的错误类型
			Code:    strconv.Itoa(statusCode), // 将数字状态码转为字符串作为 code
		},
	}
	errorJSON, err := json.Marshal(errorPayload)
	if err != nil { // 如果序列化错误响应本身失败
		Log.Errorf("sendErrorResponse: 无法序列化错误响应体: %v. 原始错误: %s", err, message)
		// 尝试发送一个最基本的纯文本错误。
		if !c.Writer.Written() { // 如果还能写头部
			// c.String(http.StatusInternalServerError, "Internal Server Error: Failed to prepare error response.")
			// 为了尽量符合JSON API规范，即使这里出错，也尝试返回JSON
			fallbackError := `{"error": {"message": "内部服务器错误: 准备错误响应失败。", "type": "internal_server_error", "code": "500"}}`
			c.Data(http.StatusInternalServerError, "application/json; charset=utf-8", []byte(fallbackError))
		}
		return
	}

	if isStream {
		// --- 发送流式错误事件 (SSE) ---
		// SSE 规范要求流本身是 200 OK，错误通过特定的事件传递。
		if !c.Writer.Written() { // 如果头部还没写（例如，请求一开始就出错，从未发送过200 OK）。
			c.Writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			c.Writer.Header().Set("Cache-Control", "no-cache")
			c.Writer.Header().Set("Connection", "keep-alive")
			c.Writer.Header().Set("X-Accel-Buffering", "no")
			// 即使发生错误，SSE 流的 HTTP 状态码也应为 200。错误信息在事件数据中。
			// 有些客户端可能对非200的SSE流处理不佳。
			// 但如果错误发生在流开始前，也可以考虑直接返回对应的HTTP错误码及JSON体。
			// OpenAI的做法似乎是即使流内错误，整体HTTP状态也是200。我们遵循此模式。
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Flush() // 确保头部已发送
		}

		Log.Debugf("sendErrorResponse: 向客户端发送 SSE 错误事件: data: %s", string(errorJSON))
		// 发送错误事件。
		if _, errFprintf := fmt.Fprintf(c.Writer, "%s%s\n\n", models.SSEDataPrefix, string(errorJSON)); errFprintf != nil {
			Log.Warnf("sendErrorResponse: 发送 SSE 错误事件失败: %v (客户端可能已断开)", errFprintf)
			return // 如果这里写入失败，客户端可能已断开，无需继续。
		}
		// 发送 [DONE] 事件来明确结束流，即使是在错误之后。
		if _, errFprintf := fmt.Fprintf(c.Writer, "%s%s\n\n", models.SSEDataPrefix, models.SSEDonePayload); errFprintf != nil {
			Log.Warnf("sendErrorResponse: 发送 SSE [DONE] 事件在错误之后失败: %v (客户端可能已断开)", errFprintf)
		}
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush() // 确保数据立即发送。
		}
	} else {
		// --- 发送非流式错误JSON ---
		if !c.Writer.Written() { // 确保响应头未被写入，这样才能设置正确的状态码和Content-Type。
			c.Data(statusCode, "application/json; charset=utf-8", errorJSON)
		} else {
			// 对于非流式，如果头已写，之前已检查过，这里只是双重保险。
			Log.Warnf("sendErrorResponse: 尝试发送 JSON 错误，但响应已写入。错误: %s", message)
		}
	}
}
