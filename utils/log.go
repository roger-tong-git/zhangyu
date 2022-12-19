package utils

import (
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"
)

func PrintError() {
	if err := recover(); err != nil {
		errMsg := "stacktrace from panic: \n" + (err.(error)).Error() + "\n" + string(debug.Stack())
		fmt.Println(errMsg)
		log.Println(errMsg)
	}
}

func SetLogFile(prefix string) {
	fileWriter := newLogFileWriter(prefix)
	mw := io.MultiWriter(fileWriter, os.Stdout)
	log.SetOutput(mw)
}
