package i18n

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	LangEN = "en"
	LangCN = "cn"
)

var current = LangEN

func Lang() string    { return current }
func IsEN() bool      { return current == LangEN }
func IsCN() bool      { return current == LangCN }
func Set(lang string) { current = normalize(lang) }

func T(key string) string {
	if m, ok := messages[current]; ok {
		if s, ok := m[key]; ok {
			return s
		}
	}
	if s, ok := messages[LangEN][key]; ok {
		return s
	}
	return key
}

func normalize(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch lang {
	case "cn", "zh", "zh-cn", "chinese", "中文":
		return LangCN
	case "en", "english", "英文":
		return LangEN
	default:
		return LangEN
	}
}

func LangFilePath(stateDir string) string {
	return filepath.Join(stateDir, "lang")
}

func LoadFromFile(stateDir string) bool {
	data, err := os.ReadFile(LangFilePath(stateDir))
	if err != nil {
		return false
	}
	lang := strings.TrimSpace(string(data))
	if lang != "" {
		Set(lang)
		return true
	}
	return false
}

func SaveToFile(stateDir, lang string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(LangFilePath(stateDir), []byte(normalize(lang)+"\n"), 0o644)
}
