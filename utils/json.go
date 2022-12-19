package utils

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
)

func GetJsonBytes(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func GetJsonString(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func GetJsonValue(v interface{}, jsonStr string) {
	_ = json.Unmarshal([]byte(jsonStr), v)
}

func GetJsonFromReader(v interface{}, reader io.Reader) error {
	if b, err := io.ReadAll(reader); err == nil {
		return json.Unmarshal(b, v)
	} else {
		return err
	}
}

func SaveJsonSetting(fileName string, setting interface{}) {
	var err error
	var b []byte
	var file *os.File
	if file, err = os.Create(fileName); err == nil {
		defer file.Close()
		writer := bufio.NewWriter(file)
		defer writer.Flush()
		str := bytes.Buffer{}
		if b, err = json.Marshal(setting); err == nil {
			_ = json.Indent(&str, b, "", "    ")
			s := str.String()
			if runtime.GOOS == "windows" {
				s = strings.ReplaceAll(s, "\n", "\r\n")
			}
			writer.Write([]byte(s))
		}
		if err != nil {
			log.Println(err)
		}
	} else {
		log.Println(err)
	}
}

func ReadJsonSetting(fileName string, setting interface{}, resetHandle func()) {
	if FileExists(fileName) {
		file, _ := os.Open(fileName)
		defer file.Close()
		reader := bufio.NewReader(file)
		resetHandle()
		json.NewDecoder(reader).Decode(&setting)
	} else {
		resetHandle()
		SaveJsonSetting(fileName, setting)
	}
}

type JsonValue struct {
	Value string
}

func (s *JsonValue) GetBytes() []byte {
	return []byte(s.Value)
}

func (s *JsonValue) GetValue(v interface{}) {
	_ = json.Unmarshal(s.GetBytes(), v)
}

func (s *JsonValue) SetValue(v interface{}) {
	b, _ := json.Marshal(v)
	str := string(b)
	s.Value = str
}

func (s *JsonValue) String() string {
	return s.Value
}
