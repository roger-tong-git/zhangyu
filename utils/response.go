package utils

type ResponseStatus string

const (
	ResponseSuccess = ResponseStatus("success")
	ResponseError   = ResponseStatus("error")
)

type ResponseBase struct {
	Status  ResponseStatus `json:"status"`
	Message string         `json:"message"`
}

func ErrResponse(message string) *ResponseBase {
	return &ResponseBase{Status: ResponseError, Message: message}
}

func SuccessResponse() *ResponseBase {
	return &ResponseBase{Status: ResponseSuccess}
}

type Response[T interface{}] struct {
	ResponseBase
	Data T
}

func NewResponse[T interface{}](message string, data T) *Response[T] {
	r := &Response[T]{Data: data}
	r.Message = message
	r.Status = ResponseSuccess
	return r
}
