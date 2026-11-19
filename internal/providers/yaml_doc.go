// providers.yaml 文档编解码：gopkg.in/yaml.v3 的结构化往返。解码侧做两件
// Rust serde 免费提供的事——version 字段存在性校验（Rust 无 default 的字段
// 缺失即解析失败）与 ProviderApp 枚举键校验（未知变体即解析失败）。
package providers

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"reins/internal/agents"
)

// rawDocumentFile 是带存在性探测的解码形态；落盘编码仍走 rawProvidersDocument
// （version 为值字段，序列化恒写）。
type rawDocumentFile struct {
	Version     *int64                             `yaml:"version"`
	Providers   map[string]rawProvider             `yaml:"providers"`
	AppSettings map[ProviderApp]ProviderAppSetting `yaml:"app_settings"`
}

func unmarshalProvidersDocument(content []byte) (*rawProvidersDocument, error) {
	var file rawDocumentFile
	if err := yaml.Unmarshal(content, &file); err != nil {
		return nil, err
	}
	if file.Version == nil {
		return nil, errors.New("缺少 version 字段")
	}
	if file.AppSettings != nil {
		validApps := map[ProviderApp]bool{}
		for _, app := range agents.ProviderApps() {
			validApps[app] = true
		}
		for app := range file.AppSettings {
			if !validApps[app] {
				return nil, fmt.Errorf("unknown provider app %q", string(app))
			}
		}
	}
	return &rawProvidersDocument{
		Version:     *file.Version,
		Providers:   file.Providers,
		AppSettings: file.AppSettings,
	}, nil
}

func marshalProvidersDocument(document *rawProvidersDocument) (string, error) {
	var builder strings.Builder
	encoder := yaml.NewEncoder(&builder)
	// serde_yaml 缺省缩进 2 空格；yaml.v3 缺省 4，显式对齐落盘形态。
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return builder.String(), nil
}
