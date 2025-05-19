// models/openai_models.go
package models

// --- OpenAI 兼容的聊天模型 ---

// Message 表示聊天会话中的单个消息。
type Message struct {
	Role    string      `json:"role"`           // 消息发送者的角色 (例如: "system", "user", "assistant", "tool")。
	Content interface{} `json:"content"`        // 消息的内容。可以是字符串 (文本消息)，也可以是对象数组 (多模态消息，例如包含图片)。
	Name    *string     `json:"name,omitempty"` // 可选：消息发送者的名字。当 role 是 "tool" 时，这是工具调用的 ID。
	// ToolCalls *[]ToolCall `json:"tool_calls,omitempty"` // 可选：由模型生成的工具调用列表 (当 role 是 "assistant" 时)。
	// ToolCallID *string `json:"tool_call_id,omitempty"` // 可选：工具调用 ID (当 role 是 "tool" 时)。
}

// ChatCompletionRequest 表示对聊天完成 API 的请求结构 (遵循 OpenAI 规范)。
type ChatCompletionRequest struct {
	Model            string    `json:"model"`                       // 必需：要使用的模型 ID (例如 "gpt-3.5-turbo")。
	Messages         []Message `json:"messages"`                    // 必需：描述迄今为止对话的消息列表。
	Temperature      *float64  `json:"temperature,omitempty"`       // 可选 (默认 1)：控制输出随机性的采样温度 (0-2)。较高值使输出更随机，较低值使其更集中和确定。
	TopP             *float64  `json:"top_p,omitempty"`             // 可选 (默认 1)：核心采样参数。模型考虑具有 top_p 概率质量的词元的结果。
	N                *int      `json:"n,omitempty"`                 // 可选 (默认 1)：为每个输入消息生成多少个聊天完成选项。
	Stream           *bool     `json:"stream,omitempty"`            // 可选 (默认 false)：如果设置，将发送部分消息增量，就像在 ChatGPT 中一样。
	MaxTokens        *int      `json:"max_tokens,omitempty"`        // 可选：聊天完成时生成的最大 token 数。
	PresencePenalty  *float64  `json:"presence_penalty,omitempty"`  // 可选 (默认 0)：对新词元根据其是否已在文本中出现进行惩罚的数字 (-2.0 到 2.0)。
	FrequencyPenalty *float64  `json:"frequency_penalty,omitempty"` // 可选 (默认 0)：对新词元根据其在文本中的现有频率进行惩罚的数字 (-2.0 到 2.0)。
	User             *string   `json:"user,omitempty"`              // 可选：代表您的最终用户的唯一标识符，可以帮助 OpenAI 监控和检测滥用行为。
	// Stop             *StringArrayOrString `json:"stop,omitempty"` // 可选：API 将停止生成更多词元的序列，最多4个。
	// LogitBias        map[string]int       `json:"logit_bias,omitempty"` // 可选：修改指定词元出现在完成中的可能性。
	// Tools            []Tool               `json:"tools,omitempty"`      // 可选：模型可能调用的工具列表。
	// ToolChoice       interface{}          `json:"tool_choice,omitempty"`// 可选：控制模型如何响应工具调用。
}

// --- OpenAI 兼容的流式响应模型 (Server-Sent Events) ---

// SSEChoiceDelta 表示在 SSE (Server-Sent Events) 流中，choices 数组内 delta 对象的内容。
// 它包含消息内容的增量部分。
// 注意：如果上游 API 的流式响应中 delta.content 也是多模态的，这里可能也需要调整。
// 但通常流式响应的 content 增量是字符串。如果遇到问题，再考虑修改。
type SSEChoiceDelta struct {
	Content *string `json:"content,omitempty"` // 消息内容的增量部分。
	Role    *string `json:"role,omitempty"`    // 角色 (通常只在流的第一个 delta 中出现，如果角色变化)。
	// ToolCalls *[]ToolCall `json:"tool_calls,omitempty"` // 工具调用增量。
}

// SSEChoice 表示在 SSE 流中，choices 数组的单个元素结构。
type SSEChoice struct {
	Delta        SSEChoiceDelta `json:"delta"`                   // 包含消息实际增量内容。
	Index        int            `json:"index"`                   // 此 choice 在所有 choices 中的索引 (当 N > 1 时有用，通常为 0)。
	FinishReason *string        `json:"finish_reason,omitempty"` // 流完成的原因 (例如 "stop", "length", "tool_calls", "content_filter")。仅在最后一个 chunk 中出现。
}

// ChatCompletionChunk 表示 SSE 流中单个事件的数据结构 (遵循 OpenAI 规范)。
type ChatCompletionChunk struct {
	ID      string      `json:"id"`      // 块的唯一 ID，通常形如 "chatcmpl-..."。
	Object  string      `json:"object"`  // 对象类型, 固定为 "chat.completion.chunk"。
	Created int64       `json:"created"` // 块创建的 Unix 时间戳 (秒)。
	Model   string      `json:"model"`   // 用于生成此块的模型 ID。
	Choices []SSEChoice `json:"choices"` // 包含消息增量的 choice 对象列表 (通常只有一个元素)。
	// SystemFingerprint *string `json:"system_fingerprint,omitempty"` // 可选：模型的系统指纹。
}

// --- OpenAI 兼容的 /v1/models 响应模型 ---

// ModelPermission 定义了与模型相关的权限。
type ModelPermission struct {
	ID                 string  `json:"id"`                   // 权限对象的唯一 ID，例如 "modelperm-..."。
	Object             string  `json:"object"`               // 对象类型, 固定为 "model_permission"。
	Created            int64   `json:"created"`              // 创建此权限的 Unix 时间戳 (秒)。
	AllowCreateEngine  bool    `json:"allow_create_engine"`  // 是否允许使用此模型创建引擎。
	AllowSampling      bool    `json:"allow_sampling"`       // 是否允许对此模型进行采样。
	AllowLogprobs      bool    `json:"allow_logprobs"`       // 是否允许获取此模型的 logprobs。
	AllowSearchIndices bool    `json:"allow_search_indices"` // 是否允许使用此模型创建搜索索引。
	AllowView          bool    `json:"allow_view"`           // 是否允许查看此模型。
	AllowFineTuning    bool    `json:"allow_fine_tuning"`    // 是否允许对此模型进行微调。
	Organization       string  `json:"organization"`         // 此权限所属的组织 (通常是 "*" 表示所有组织)。
	Group              *string `json:"group,omitempty"`      // 可选：权限所属的组。
	IsBlocking         bool    `json:"is_blocking"`          // 此权限是否是阻塞性的。
}

// ModelData 定义了单个模型的信息。
type ModelData struct {
	ID          string            `json:"id"`               // 模型的唯一 ID (例如 "gpt-3.5-turbo")。
	Object      string            `json:"object"`           // 对象类型, 固定为 "model"。
	Created     int64             `json:"created"`          // 模型创建的 Unix 时间戳 (秒)。
	OwnedBy     string            `json:"owned_by"`         // 模型的所有者 (例如 "openai", "openrouter", "system")。
	Permissions []ModelPermission `json:"permission"`       // 与此模型相关的权限列表 (注意 OpenAI API schema 使用单数 "permission" 作为键，但内容是数组)。
	Root        string            `json:"root"`             // 此模型的根模型 ID。
	Parent      *string           `json:"parent,omitempty"` // 可选：此模型的父模型 ID。
}

// ListModelsResponse 表示 /v1/models 端点的响应结构。
type ListModelsResponse struct {
	Object string      `json:"object"` // 对象类型, 固定为 "list"。
	Data   []ModelData `json:"data"`   // 包含模型数据对象的列表。
}

// --- OpenRouter 特定的 /models 响应模型 ---
// 这些结构用于解析从 OpenRouter /api/v1/models 端点获取的原始数据。
// 然后这些数据会被转换为上面的 OpenAI 兼容的 ModelData 结构。

// OpenRouterModel 表示从 OpenRouter 的 /models API 获取的单个模型对象。
type OpenRouterModel struct {
	ID            string      `json:"id"`             // 模型 ID, 例如 "openai/gpt-3.5-turbo"
	Name          string      `json:"name"`           // 人类可读的模型名称, 例如 "GPT-3.5 Turbo"
	Description   string      `json:"description"`    // 模型描述
	Pricing       interface{} `json:"pricing"`        // 定价信息 (可以是 map[string]string 或更复杂的结构，具体结构取决于 OpenRouter 的 API)
	ContextLength int         `json:"context_length"` // 模型支持的最大上下文长度 (token 数)
	Architecture  *struct { // 可选：模型的架构信息
		Modality        string `json:"modality"`         // 模型处理的模态 (例如 "text", "image")
		Tokenizer       string `json:"tokenizer"`        // 使用的 tokenizer (例如 "gpt2", "claude")
		DefaultTemplate string `json:"default_template"` // 默认的提示模板
	} `json:"architecture,omitempty"`
	TopProvider *struct { // 可选：模型的主要提供商信息
		ProviderID string `json:"provider_id"` // 提供商的 ID (例如 "openai")
		IsDefault  bool   `json:"is_default"`  // 是否为默认提供商
	} `json:"top_provider,omitempty"`
	PerRequestLimits *map[string]int `json:"per_request_limits,omitempty"` // 可选：特定于请求的限制 (例如每分钟请求数)
	// 可以根据 OpenRouter /models 响应的具体内容添加更多字段，例如 "tags", "is_free" 等。
}

// OpenRouterModelsResponse 表示从 OpenRouter 的 /models API 获取的顶级响应结构。
type OpenRouterModelsResponse struct {
	Data []OpenRouterModel `json:"data"` // 包含 OpenRouterModel 对象的列表。
}
