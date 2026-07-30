// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package i18n

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	gotemplate "text/template"

	"github.com/BurntSushi/toml"
	"golang.org/x/text/language"

	"github.com/openbkn-ai/bkn-comm-go/logger"
)

type Message struct {
	Data string

	parseOnce      sync.Once
	parsedTemplate *gotemplate.Template
}

var (
	iLocalizer = make(map[string]map[string]*Message)
	leftDelim  = "{{"
)

// 语言类型map
func RegisterI18n(localeDir string) {
	// get locale file list
	fileInfos, err := os.ReadDir(localeDir)
	if err != nil {
		logger.Fatalf("load locale dir %s failed: %v", localeDir, err)
	}

	for _, fileInfos := range fileInfos {
		// filename format must be <module>.<language>.toml
		s := strings.Split(fileInfos.Name(), ".")
		if len(s) == 2 && s[1] == "go" {
			continue
		}
		if len(s) != 3 || s[2] != "toml" {
			logger.Fatalf("locale file %s filename format error, correct format is <module>.<language>.toml", fileInfos.Name())
			return
		}

		lang := s[1]
		language.MustParse(lang)
		if iLocalizer[lang] == nil {
			iLocalizer[lang] = make(map[string]*Message)
		}

		filename := path.Join(localeDir, fileInfos.Name())
		logger.Infof("load locale file: %s", filename)

		buf, err := os.ReadFile(filename)
		if err != nil {
			logger.Fatalf("load locale file %s failed: %v", filename, err)
			return
		}

		var raw interface{}
		if err = toml.Unmarshal(buf, &raw); err != nil {
			logger.Fatalf("Unmarshal locale file %s failed: %v", filename, err)
			return
		}

		if err = recGetMessages(lang, "", raw); err != nil {
			logger.Fatalf("recGetMessages failed: %v", err)
			return
		}
	}

	if err := checkLanguageMap(); err != nil {
		logger.Fatalf(err.Error())
	}
}

func checkLanguageMap() error {

	first := true
	var firstLang string
	var firstMap map[string]*Message
	for lang, mp := range iLocalizer {
		if first {
			first = false
			firstLang = lang
			firstMap = mp
			continue
		}

		if len(mp) != len(firstMap) {
			return fmt.Errorf("%s(%d) map length is not equal to %s(%d)", lang, len(mp), firstLang, len(firstMap))
		}

		for k := range firstMap {
			if mp[k] == nil {
				return fmt.Errorf("%s map is not equal to %s, missing messageId %s", lang, firstLang, k)
			}
		}
	}

	return nil
}

func recGetMessages(lang string, messageId string, raw interface{}) error {
	switch data := raw.(type) {
	case string:
		if data == "" {
			logger.Fatalf("messageId %s is empty string", messageId)
		}
		if oldMessage, ok := iLocalizer[lang][messageId]; ok {
			logger.Fatalf("messageId %s already exist, old data: %s, new data: %s", messageId, oldMessage.Data, data)
		}
		iLocalizer[lang][messageId] = &Message{
			Data: data,
		}

	case map[string]interface{}:
		for k, v := range data {
			// recursively scan map items
			if messageId != "" {
				k = messageId + "." + k
			}
			err := recGetMessages(lang, k, v)
			if err != nil {
				return err
			}
		}

	default:
		return fmt.Errorf("unsupported data format %T: %v", raw, data)
	}

	return nil
}

// 根据语言获取对应的国际化内容
func Translate(lang string, messageId string, templateDate map[string]interface{}) string {
	localizer, ok := iLocalizer[lang]
	if !ok {
		logger.Fatalf("the localizer of %s is not exist", lang)
		return ""
	}

	message, ok := localizer[messageId]
	if !ok {
		logger.Fatalf("the messageId %s in localizer %s is not exist", messageId, lang)
		return ""
	}

	if !strings.Contains(message.Data, leftDelim) {
		return message.Data
	}

	var err error
	message.parseOnce.Do(func() {
		message.parsedTemplate, err = gotemplate.New("").Parse(message.Data)
		if err != nil {
			logger.Fatalf("messageId %s in localizer %s is incorrect, failed to parse the message, message data is '%s'", messageId, lang, message.Data)
		}
	})

	var buf bytes.Buffer
	if err := message.parsedTemplate.Execute(&buf, templateDate); err != nil {
		logger.Fatalf("messageId %s in localizer %s is incorrect, failed to execute the message, message data is '%s', template data is %v", messageId, lang, message.Data, templateDate)
		return ""
	}
	return buf.String()
}
