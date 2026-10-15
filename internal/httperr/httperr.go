// Package httperr 定义带 HTTP 状态码的业务错误，供服务层构造、路由层统一映射为响应。
package httperr

// Error 是带状态码的业务错误；Message 可直接展示给调用方（不含敏感值）。
type Error struct {
	// Status 是对外 HTTP 状态码。
	Status int
	// Message 是中文错误文案。
	Message string
}

// Error 实现 error 接口。
func (e *Error) Error() string { return e.Message }

// New 构造业务错误。
func New(status int, message string) *Error { return &Error{Status: status, Message: message} }
