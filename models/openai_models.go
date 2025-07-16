// models/openai_models.go
package models

// --- OpenAI 兼容的聊天模型 ---

// 【新增】FunctionParameters 定义了函数调用中的参数结构，遵循JSON Schema规范。
type FunctionParameters struct {
	Type       string                        `json:"type"`                 // 通常是 "object"
	Properties map[string]FunctionProperty `json:"properties"`             // 参数属性的映射
	Required   []string                      `json:"required,omitempty"`   // 必需的参数列表
}

// 【新增】FunctionProperty 定义了单个函数参数的属性。
type FunctionProperty struct {
	Type        string   `json:"type"`                  // 参数类型，如 "string", "number", "boolean"
	Description string   `json:"description,omitempty"` // 参数描述
	Enum        []string `json:"enum,omitempty"`        // 如果类型是字符串，可以提供一个可选值的枚举
}

// 【新增】FunctionDefinition 定义了单个函数的描述。
type FunctionDefinition struct {
	Name        string              `json:"name"`               // 函数名称
	Description string              `json:"description"`        // 函数功能的描述
	Parameters  FunctionParameters  `json:"parameters"`         // 函数的参数
}

// 【新增】Tool 定义了一个可供模型使用的工具。目前只支持 "function" 类型。
type Tool struct {
	Type     string             `json:"type"`      // 工具类型，目前固定为 "function"
	Function FunctionDefinition `json:"function"`  // 函数的具体定义
}

// 【新增】ToolCall 是模型决定要调用的一个具体工具实例。
type ToolCall struct {
	ID       string         `json:"id"`                 // 唯一的调用ID，用于后续关联结果
	Type     string         `json:"type"`               // 工具类型，固定为 "function"
	Function ToolCallFunction `json:"function"`           // 要调用的函数及其参数
}

// 【新增】ToolCallFunction 定义了要调用的函数名称和参数。
type ToolCallFunction struct {
	Name      string `json:"name"`      // 要调用的函数名称
	Arguments string `json:"arguments"` // 一个JSON字符串，包含了调用该函数的参数
}

// Message 表示聊天会话中的单个消息。
// 【修改】为 Message 添加 tool_calls 和 tool_call_id 字段
type Message struct {
	Role       string      `json:"role"`                     // 消息发送者的角色 (e.g., "system", "user", "assistant", "tool")
	Content    interface{} `json:"content"`                  // 消息的内容。可以是字符串或对象数组（多模态）。
	Name       *string     `json:"name,omitempty"`           // 当 role 是 "tool" 时，可以是工具名称
	ToolCalls  *[]ToolCall `json:"tool_calls,omitempty"`     // 【新增】由模型生成的工具调用列表 (当 role 是 "assistant" 时)
	ToolCallID *string     `json:"tool_call_id,omitempty"`   // 【新增】工具调用 ID (当 role 是 "tool" 时，用于关联结果)
}

// ChatCompletionRequest 表示对聊天完成 API 的请求结构 (遵循 OpenAI 规范)。
// 【修改】为 ChatCompletionRequest 添加 tools 和 tool_choice 字段
type ChatCompletionRequest struct {
	Model            string      `json:"model"`                         // 必需：要使用的模型 ID
	Messages         []Message   `json:"messages"`                      // 必需：消息列表
	Temperature      *float64    `json:"temperature,omitempty"`         // 可选
	TopP             *float64    `json:"top_p,omitempty"`               // 可选
	N                *int        `json:"n,omitempty"`                   // 可选
	Stream           *bool       `json:"stream,omitempty"`              // 可选
	MaxTokens        *int        `json:"max_tokens,omitempty"`          // 可选
	PresencePenalty  *float64    `json:"presence_penalty,omitempty"`    // 可选
	FrequencyPenalty *float64    `json:"frequency_penalty,omitempty"`   // 可选
	User             *string     `json:"user,omitempty"`                // 可选
	Tools            *[]Tool     `json:"tools,omitempty"`               // 【新增】模型可用的工具列表
	ToolChoice       interface{} `json:"tool_choice,omitempty"`         // 【新增】控制模型如何响应工具调用 (可以是 "none", "auto", 或 {"type": "function", "function": {"name": "my_function"}})
}

// --- OpenAI 兼容的流式响应模型 (Server-Sent Events) ---

// SSEChoiceDelta 表示在 SSE 流中，choices 数组内 delta 对象的内容。
// 【修改】为 SSEChoiceDelta 添加 tool_calls 字段
type SSEChoiceDelta struct {
	Content   *string     `json:"content,omitempty"`      // 消息内容的增量部分
	Role      *string     `json:"role,omitempty"`         // 角色
	ToolCalls *[]ToolCall `json:"tool_calls,omitempty"`   // 【新增】工具调用增量
}

// SSEChoice 表示在 SSE 流中，choices 数组的单个元素结构。
type SSEChoice struct {
	Delta        SSEChoiceDelta `json:"delta"`
	Index        int            `json:"index"`
	FinishReason *string        `json:"finish_reason,omitempty"` // 包括 "tool_calls"
}

// ChatCompletionChunk 表示 SSE 流中单个事件的数据结构。
type ChatCompletionChunk struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"` // "chat.completion.chunk"
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []SSEChoice `json:"choices"`
}

// --- OpenAI 兼容的 /v1/models 响应模型 (这部分不需要修改) ---

type ModelPermission struct {
	ID                 string  `json:"id"`
	Object             string  `json:"object"`
	Created            int64   `json:"created"`
	AllowCreateEngine  bool    `json:"allow_create_engine"`
	AllowSampling      bool    `json:"allow_sampling"`
	AllowLogprobs      bool    `json:"allow_logprobs"`
	AllowSearchIndices bool    `json:"allow_search_indices"`
	AllowView          bool    `json:"allow_view"`
	AllowFineTuning    bool    `json:"allow_fine_tuning"`
	Organization       string  `json:"organization"`
	Group              *string `json:"group,omitempty"`
	IsBlocking         bool    `json:"is_blocking"`
}

type ModelData struct {
	ID          string            `json:"id"`
	Object      string            `json:"object"`
	Created     int64             `json:"created"`
	OwnedBy     string            `json:"owned_by"`
	Permissions []ModelPermission `json:"permission"`
	Root        string            `json:"root"`
	Parent      *string           `json:"parent,omitempty"`
}

type ListModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelData `json:"data"`
}

// --- OpenRouter 特定的 /models 响应模型 (这部分不需要修改) ---

type OpenRouterModel struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Description      string      `json:"description"`
	Pricing          interface{} `json:"pricing"`
	ContextLength    int         `json:"context_length"`
	Architecture     *struct {
		Modality        string `json:"modality"`
		Tokenizer       string `json:"tokenizer"`
		DefaultTemplate string `json:"default_template"`
	} `json:"architecture,omitempty"`
	TopProvider      *struct {
		ProviderID string `json:"provider_id"`
		IsDefault  bool   `json:"is_default"`
	} `json:"top_provider,omitempty"`
	PerRequestLimits *map[string]int `json:"per_request_limits,omitempty"`
}

type OpenRouterModelsResponse struct {
	Data []OpenRouterModel `json:"data"`
}
