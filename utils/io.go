package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func FileExists(path string) bool {
	_, err := os.Stat(path) //os.Stat获取文件信息
	if err != nil {
		if os.IsExist(err) {
			return true
		}
		return false
	}
	return true
}

type logFileWriter struct {
	logFileWriterLock sync.Mutex
	filePrefix        string
}

func newLogFileWriter(filePrefix string) *logFileWriter {
	return &logFileWriter{filePrefix: filePrefix}
}

func (l *logFileWriter) Write(p []byte) (n int, err error) {
	defer l.logFileWriterLock.Unlock()
	l.logFileWriterLock.Lock()

	dir := filepath.Join(filepath.Dir(os.Args[0]), "logs")
	if !FileExists(dir) {
		if err = os.MkdirAll(dir, 755); err != nil {
			println(err)
			return 0, err
		}
	}

	prefix := ""
	if l.filePrefix != "" {
		prefix = l.filePrefix + "-"
	}
	fileName := filepath.Join(dir, fmt.Sprintf("%s%s.log", prefix, time.Now().Format("2006-01-02")))
	var logfile *os.File

	if !FileExists(fileName) {
		logfile, err = os.Create(fileName)
		if err != nil {
			println(err)
			return 0, err
		}
	} else {
		logfile, err = os.OpenFile(fileName, os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			println(err)
			return 0, err
		}
	}
	defer func() {
		_ = logfile.Close()
	}()

	return logfile.Write(p)
}
