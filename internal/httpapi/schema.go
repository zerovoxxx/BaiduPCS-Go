package httpapi

import (
	"encoding/json"
	"strconv"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
)

type schemaField struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Unit        *string  `json:"unit"`
	Default     any      `json:"default_value"`
	Minimum     *string  `json:"minimum"`
	Maximum     *string  `json:"maximum"`
	EnumValues  []string `json:"enum_values"`
	Effect      string   `json:"effect"`
	Secret      bool     `json:"secret"`
	Nullable    bool     `json:"nullable"`
	Description string   `json:"description"`
}
type configSchema struct {
	Fields     []schemaField `json:"fields"`
	Additional bool          `json:"additional_properties"`
}

func configurationSchema(document []byte, defaults clientDTO) (configSchema, error) {
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Type        string   `json:"type"`
					Minimum     *int64   `json:"minimum"`
					Maximum     *int64   `json:"maximum"`
					Enum        []string `json:"enum"`
					Description string   `json:"description"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(document, &spec); err != nil {
		return configSchema{}, err
	}
	encoded, _ := json.Marshal(defaults)
	values := map[string]any{}
	decoder := json.Unmarshal(encoded, &values)
	if decoder != nil {
		return configSchema{}, decoder
	}
	result := configSchema{Fields: []schemaField{}}
	for _, name := range configuration.Fields {
		property := spec.Components.Schemas["ClientConfiguration"].Properties[name]
		field := schemaField{Name: name, Type: property.Type, Default: values[name], EnumValues: append([]string{}, property.Enum...), Effect: "new_job", Description: property.Description}
		if field.Type == "" {
			field.Type = "string"
		}
		if property.Minimum != nil {
			v := strconv.FormatInt(*property.Minimum, 10)
			field.Minimum = &v
		}
		if property.Maximum != nil {
			v := strconv.FormatInt(*property.Maximum, 10)
			field.Maximum = &v
		}
		switch name {
		case "cache_size":
			field.Type = "decimal_string"
			unit, min, max := "bytes", "1024", strconv.FormatUint(uint64(^uint(0)>>1), 10)
			field.Unit = &unit
			field.Minimum = &min
			field.Maximum = &max
		case "max_download_rate", "max_upload_rate":
			field.Type = "decimal_string"
			unit, min, max := "bytes_per_second", "0", "9223372036854775807"
			field.Unit = &unit
			field.Minimum = &min
			field.Maximum = &max
		case "proxy":
			field.Type = "write_only_url"
			field.Secret = true
			field.Default = ""
		case "savedir":
			field.Type = "local_directory"
		case "local_addrs", "proxy_hostnames":
			field.Type = "string_array"
		case "max_parallel", "max_upload_parallel", "max_download_load", "max_upload_load":
			unit := "count"
			field.Unit = &unit
		}
		if field.Description == "" {
			field.Description = "提交新任务时固定的客户端默认值"
		}
		result.Fields = append(result.Fields, field)
	}
	return result, nil
}
