package winshell

import "testing"

// 以 .14 實際讀到的安全描述元判斷本人能不能以一般權限改寫、刪除排程。
func TestTaskWritableBy(t *testing.T) {
	const me = "S-1-5-21-143378739-3327937081-2037047593-1001"
	const other = "S-1-5-21-143378739-3327937081-2037047593-1013"
	inherited := "(A;ID;0x1f019f;;;BA)(A;ID;0x1f019f;;;SY)(A;ID;FA;;;BA)"
	for _, c := range []struct {
		name, sddl string
		want       bool
	}{
		// 0.2.11 以前提權的安裝程式建立：使用者只能讀。
		{"提權預設", "O:BAG:S-1-5-21-143378739-3327937081-2037047593-513D:" + inherited + "(A;;FR;;;" + me + ")", false},
		// 提權時傳 TaskOpenSDDL：預設之外多一筆本人完全控制。
		{"提權開放", "O:BAG:S-1-5-21-143378739-3327937081-2037047593-513D:(A;;FA;;;" + me + ")" + inherited + "(A;;FR;;;" + me + ")", true},
		// 使用者自己建立（擁有者是本人，經 CREATOR OWNER 繼承完全控制），與 OneDrive 的每位使用者排程相同。
		{"本人建立", "O:" + me + "G:S-1-5-21-143378739-3327937081-2037047593-513D:(A;ID;0x1f019f;;;BA)(A;ID;0x1f019f;;;SY)(A;ID;FA;;;" + me + ")(A;;FR;;;" + me + ")", true},
		{"開放給別人", "O:BAD:" + inherited + "(A;;FA;;;" + other + ")(A;;FR;;;" + me + ")", false},
		{"遮罩寫法", "O:BAD:(A;;0x1f01ff;;;" + me + ")", true},
		{"拒絕不算", "O:BAD:(D;;FA;;;" + me + ")", false},
		{"只給子物件繼承不算", "O:BAD:(A;OICIIO;FA;;;" + me + ")", false},
		{"擁有者不等於能寫", "O:" + me + "D:" + inherited, false},
		{"沒有 DACL", "O:" + me, false},
		{"大小寫", "D:(A;;fa;;;" + "s-1-5-21-143378739-3327937081-2037047593-1001" + ")", true},
	} {
		if got := TaskWritableBy(c.sddl, me); got != c.want {
			t.Errorf("%s：%q → %v，應為 %v", c.name, c.sddl, got, c.want)
		}
	}
	if TaskWritableBy("D:(A;;FA;;;"+me+")", "") {
		t.Error("沒有 SID 時不可當作能寫")
	}
	if got := TaskOpenSDDL(me); got != "D:(A;;FA;;;"+me+")" {
		t.Errorf("TaskOpenSDDL = %q", got)
	}
}
