package utils

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
)

func GetRealRemoteAddr(r *http.Request) (xforAddr string) {
	//xforAddr = r.Header.Get("X-Forwarded-Ip")
	////if xforAddr != "" {
	////	return
	////}

	xforAddr = r.Header.Get("X-Client-IP")
	if xforAddr != "" {
		return
	}

	//xforAddr = r.Header.Get("HTTP-CLIENT-IP")
	//if xforAddr != "" {
	//	return
	//}
	//
	//xforAddr = r.Header.Get("x-Original-Forwarded-For")
	//if xforAddr != "" {
	//	return
	//}
	//
	//xforAddr = r.Header.Get("X-Forwarded-For")
	//if xforAddr != "" {
	//	return
	//}

	xforAddr = r.Header.Get("X-Real-IP")
	if xforAddr != "" {
		return
	}

	return r.RemoteAddr
}

func WriteResponse(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(msg))
}

type HttpInfo struct {
	header http.Header
}

func (s *HttpInfo) IsJson() bool {
	return strings.Contains(strings.ToLower(s.header.Get("Content-Type")), "json")
}

func (s *HttpInfo) IsGzip() bool {
	return strings.Contains(s.header.Get("Content-Encoding"), "gzip")
}

type HttpReader struct {
	reader     io.Reader
	gzipReader *gzip.Reader
	HttpInfo
}

func NewRequestReader(r *http.Request) *HttpReader {
	re := &HttpReader{reader: r.Body}
	re.header = r.Header
	return re
}

func NewResponseReader(r *http.Response) *HttpReader {
	re := &HttpReader{reader: r.Body}
	re.header = r.Header
	return re
}

func (s *HttpReader) Read(p []byte) (n int, err error) {
	if s.IsGzip() {
		if s.gzipReader == nil {
			s.gzipReader, _ = gzip.NewReader(s.reader)
		}
		return s.gzipReader.Read(p)
	} else {
		return s.reader.Read(p)
	}
}

type HttpWriter struct {
	writer     io.Writer
	gzipWriter *gzip.Writer
	HttpInfo
}

func (s *HttpWriter) Write(p []byte) (n int, err error) {
	if s.IsGzip() {
		if s.gzipWriter == nil {
			s.gzipWriter = gzip.NewWriter(s.writer)
		}

		if n, err = s.gzipWriter.Write(p); err == nil {
			_ = s.gzipWriter.Flush()
		}
		return
	} else {
		return s.writer.Write(p)
	}
}

func NewRequestWriter(r *http.Request, writer io.Writer) *HttpWriter {
	re := &HttpWriter{writer: writer}
	re.header = r.Header
	return re
}

func NewResponseWriter(r *http.Response, writer io.Writer) *HttpWriter {
	re := &HttpWriter{writer: writer}
	re.header = r.Header
	return re
}
