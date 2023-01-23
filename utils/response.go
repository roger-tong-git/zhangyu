package utils

type ResponseStatus string

const (
	ResponseSuccess = ResponseStatus("success")
	ResponseError   = ResponseStatus("error")
)

type Response struct {
	Status  ResponseStatus `json:"status"`
	Message string         `json:"message,omitempty"`
	Data    any            `json:"data,omitempty"`
}

func ErrResponse(message string) *Response {
	return &Response{Status: ResponseError, Message: message}
}

func SuccessResponse(data any) *Response {
	return &Response{Status: ResponseSuccess, Data: data}
}
