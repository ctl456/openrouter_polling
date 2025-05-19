package utils

// SafeSuffix 辅助函数，用于安全地获取字符串末尾的指定长度的子串，并添加前缀 "..."。
// 主要用于日志或显示，避免暴露完整的敏感信息（如API密钥）。
// 例如，SafeSuffix("sk-abcdefghijklmnopqrstuvwxyz") 返回 "...wxyz" (假设内部 suffixLength 为 4)。
// s: 输入字符串。
// 返回: 处理后的字符串，或在输入为空时返回 "[EMPTY]"。
func SafeSuffix(s string) string {
	const suffixLength = 4 // 定义要显示的末尾字符数量。可以考虑将其作为参数或配置。
	if len(s) == 0 {
		return "[EMPTY]" // 如果字符串为空
	}
	if len(s) > suffixLength {
		return "..." + s[len(s)-suffixLength:] // 返回 "..." 加上末尾N位
	}
	// 如果字符串长度小于等于 suffixLength 但不为空
	// 为了与长字符串的 "...suffix" 格式一致，短字符串也显示 "...string"
	// 如果希望短字符串完整显示，可以改为: return s
	return "..." + s
}

// DerefString 安全地解引用字符串指针。
// 如果指针为 nil，则返回提供的默认字符串值。
// 如果指针不为 nil，则返回指针指向的字符串值。
// 这对于处理来自 JSON 请求等可能为 nil 的可选字符串字段非常有用。
// s: 指向字符串的指针。
// def: 如果 s 为 nil 时要返回的默认字符串。
// 返回: 解引用后的字符串或默认值。
func DerefString(s *string, def string) string {
	if s != nil {
		return *s
	}
	return def
}
