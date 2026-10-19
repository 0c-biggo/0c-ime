package vocabapi

import (
	"maps"
	"reflect"
	"slices"
	"strings"
)

// schemaOf 從 Go 型別產生 JSON Schema（OpenAPI 3.1 與 MCP 工具共用）。只處理這個 API 用得到的型別：
// 字串、整數、浮點數、布林、切片、map[string]T、結構（依 json 標籤，desc 標籤是說明）與指標。
func schemaOf(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaOf(t.Elem())}
	case reflect.Struct:
		props := map[string]any{}
		var required []string
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			s := schemaOf(f.Type)
			if d := f.Tag.Get("desc"); d != "" {
				s["description"] = d
			}
			props[name] = s
			if !strings.Contains(opts, "omitempty") && !strings.Contains(opts, "omitzero") && f.Type.Kind() != reflect.Pointer {
				required = append(required, name)
			}
		}
		out := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	}
	return map[string]any{}
}

func inputSchema(op Op) map[string]any {
	if op.Input == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return schemaOf(reflect.TypeOf(op.Input))
}

func outputSchema(op Op) map[string]any { return schemaOf(reflect.TypeOf(op.Output)) }

// errorSchema 是錯誤回應的形狀（vocab.Error）。
var errorSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"error": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code":    map[string]any{"type": "string", "description": "錯誤代碼，例如 invalid_word、invalid_reading、limit_words、limit_batch、not_found、conflict"},
				"item":    map[string]any{"type": "integer", "description": "批次中第幾筆出錯（從 0 起）；與單筆無關時為 -1"},
				"text":    map[string]any{"type": "string"},
				"message": map[string]any{"type": "string", "description": "給人讀的說明，可以直接轉告使用者"},
				"limit":   map[string]any{"type": "integer"},
				"left":    map[string]any{"type": "integer", "description": "還能加幾筆"},
			},
		},
	},
}

// OpenAPI 回傳 OpenAPI 3.1 文件。
func OpenAPI(baseURL string) map[string]any {
	paths := map[string]any{}
	for _, op := range Ops {
		item, _ := paths[op.Path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[op.Path] = item
		}
		o := map[string]any{
			"operationId": op.Name,
			"summary":     op.Summary,
			"description": op.Description,
			"security":    []any{map[string]any{"bearer": []any{}}},
			"responses": map[string]any{
				"200": map[string]any{"description": "成功", "content": map[string]any{"application/json": map[string]any{"schema": outputSchema(op)}}},
				"400": map[string]any{"description": "輸入不合格或超過上限，整批沒有寫入", "content": map[string]any{"application/json": map[string]any{"schema": errorSchema}}},
				"401": map[string]any{"description": "沒有帶 token 或 token 錯誤"},
				"503": map[string]any{"description": "輸入法引擎正在切換（更新中），稍後重試"},
			},
		}
		if op.Input != nil {
			if op.Method == "GET" {
				var params []any
				props := inputSchema(op)["properties"].(map[string]any)
				for _, name := range sortedKeys(props) {
					s := props[name].(map[string]any)
					params = append(params, map[string]any{"name": name, "in": "query", "description": s["description"], "schema": map[string]any{"type": s["type"]}})
				}
				o["parameters"] = params
			} else {
				o["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": inputSchema(op)}}}
			}
		}
		item[strings.ToLower(op.Method)] = o
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "靈犀注音 個人詞庫 API",
			"version": "1",
			"description": "在這台電腦上管理靈犀注音的自訂詞與「我的片語」。資料端點要加標頭 Authorization: Bearer <token>（在靈犀注音設定「我的詞彙」頁取得）。" +
				"只能動使用者親手加入的詞與片語，看不到自動學到的詞與輸入紀錄。每次修改是一個批次，可以用 undo_batch 撤回。" +
				"破音字請先用 suggest_readings 查，依語境挑不出讀音時問使用者。英文詞（ls、kubectl 這類指令）不需要讀音，中英混打時照英文送出。同樣的操作也有 MCP 工具（/mcp）。",
		},
		"servers":    []any{map[string]any{"url": baseURL}},
		"paths":      paths,
		"components": map[string]any{"securitySchemes": map[string]any{"bearer": map[string]any{"type": "http", "scheme": "bearer"}}},
	}
}

func sortedKeys(m map[string]any) []string { return slices.Sorted(maps.Keys(m)) }
