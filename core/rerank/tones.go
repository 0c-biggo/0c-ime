package rerank

// 選字序列模型套件的 schema。v2 另外宣告 tone_policy，接受逐音節的未知聲調（models zuin-tone-mask-v1）；
// v1（PF512）只收打了聲調的讀音。
//
// 這裡只有桌面載入 v2 套件需要的部分（#281）：桌面不開未知聲調，打了聲調的請求在 v1、v2 套件上相同。
// 手機的未知聲調（UnknownTonesDeclared 之外的 Options.UnknownTones、Bases、ParseBases 等）在
// feat/zhuyin-abbreviation 的 16cf6cae；那邊併進來時以它的 tones.go 為準，這裡的定義是它的子集、內容相同。
const (
	SequenceSchemaV1 = "zuin-sequence-runtime-v1"
	SequenceSchemaV2 = "zuin-sequence-runtime-v2"
	ToneMaskSchema   = "zuin-tone-mask-v1"
)

// TonePolicy 是套件 manifest 的 tone_policy。UnknownToneID 用指標區分「沒寫」與 0。
type TonePolicy struct {
	Schema        string `json:"schema"`
	UnknownToneID *int   `json:"unknown_tone_id"`
}

// UnknownTonesDeclared 回報套件是否宣告接受未知聲調：schema 是 v2，而且 tone_policy 是
// zuin-tone-mask-v1、unknown_tone_id 為 0。其他組合（含 PF512 的 v1）一律不開，沒打聲調的段落照統計引擎。
func UnknownTonesDeclared(schema string, policy *TonePolicy) bool {
	return schema == SequenceSchemaV2 && policy != nil && policy.Schema == ToneMaskSchema &&
		policy.UnknownToneID != nil && *policy.UnknownToneID == 0
}

// ValidTonePolicy 回報 schema 與 tone_policy 的組合是不是 worker 會接受的：v1 不能帶 tone_policy，
// v2 一定要帶合法的 tone_policy（同 models sequence_runtime.py 的檢查）。其他 schema 不帶 tone_policy。
func ValidTonePolicy(schema string, policy *TonePolicy) bool {
	if schema == SequenceSchemaV2 {
		return UnknownTonesDeclared(schema, policy)
	}
	return policy == nil
}
