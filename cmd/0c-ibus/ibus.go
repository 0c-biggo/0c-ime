//go:build linux

package main

// IBus 在 D-Bus 上以「序列化物件」傳遞文字、候選表與選單屬性：每個物件是
// (型別名稱, 附件 a{sv}, 欄位...) 的 struct，包在 variant 裡。欄位順序照
// libibus 的 *_serialize（ibustext.c、ibuslookuptable.c、ibusproperty.c）。

import "github.com/godbus/dbus/v5"

const (
	componentName = "org.biggo.ime.IBus"
	engineName    = "0c"
	factoryPath   = dbus.ObjectPath("/org/freedesktop/IBus/Factory")
	enginePrefix  = "/org/freedesktop/IBus/Engine/"

	ifaceEngine     = "org.freedesktop.IBus.Engine"
	ifaceFactory    = "org.freedesktop.IBus.Factory"
	ifaceService    = "org.freedesktop.IBus.Service"
	ifaceProperties = "org.freedesktop.DBus.Properties"
	ifaceIntrospect = "org.freedesktop.DBus.Introspectable"
)

// 修飾鍵（IBusModifierType）。
const (
	maskShift   = 1 << 0
	maskLock    = 1 << 1 // Caps Lock
	maskControl = 1 << 2
	maskMod1    = 1 << 3 // Alt
	maskMod4    = 1 << 6
	maskSuper   = 1 << 26
	maskHyper   = 1 << 27
	maskMeta    = 1 << 28
	maskRelease = 1 << 30
)

// 用戶端能力（IBusCapabilite）與欄位用途（IBusInputPurpose／IBusInputHints）。
const (
	capSurroundingText = 1 << 5

	purposePassword = 8
	purposePIN      = 9
	hintPrivate     = 1 << 11
)

const (
	attrUnderline  = 1
	attrForeground = 2
	attrBackground = 3

	underlineSingle = 1
	underlineDouble = 2
	underlineError  = 4

	// 失焦、重設或切換輸入法時由 ibus-daemon／用戶端把組字提交出去，引擎不另外提交。
	preeditCommit = 1

	orientationVertical = 1

	propNormal = 0
	propToggle = 1 // 可以打勾的項目（IBusProperty 的 PROP_TYPE_TOGGLE）

	propStateChecked = 1
)

type attribute struct{ typ, value, start, end uint32 }

type ibusAttribute struct {
	Name                    string
	Attach                  map[string]dbus.Variant
	Type, Value, Start, End uint32
}

type ibusAttrList struct {
	Name   string
	Attach map[string]dbus.Variant
	Attrs  []dbus.Variant
}

type ibusText struct {
	Name   string
	Attach map[string]dbus.Variant
	Text   string
	Attrs  dbus.Variant
}

type ibusLookupTable struct {
	Name          string
	Attach        map[string]dbus.Variant
	PageSize      uint32
	Cursor        uint32
	CursorVisible bool
	Round         bool
	Orientation   int32
	Candidates    []dbus.Variant
	Labels        []dbus.Variant
}

type ibusProperty struct {
	Name      string
	Attach    map[string]dbus.Variant
	Key       string
	Type      uint32
	Label     dbus.Variant
	Icon      string
	Tooltip   dbus.Variant
	Sensitive bool
	Visible   bool
	State     uint32
	SubProps  dbus.Variant
	Symbol    dbus.Variant
}

type ibusPropList struct {
	Name   string
	Attach map[string]dbus.Variant
	Props  []dbus.Variant
}

func newText(s string, attrs ...attribute) dbus.Variant {
	list := ibusAttrList{Name: "IBusAttrList", Attach: map[string]dbus.Variant{}, Attrs: []dbus.Variant{}}
	for _, a := range attrs {
		list.Attrs = append(list.Attrs, dbus.MakeVariant(ibusAttribute{Name: "IBusAttribute",
			Attach: map[string]dbus.Variant{}, Type: a.typ, Value: a.value, Start: a.start, End: a.end}))
	}
	return dbus.MakeVariant(ibusText{Name: "IBusText", Attach: map[string]dbus.Variant{}, Text: s,
		Attrs: dbus.MakeVariant(list)})
}

// newLookupTable 只放目前這一頁：翻頁與反白由核心決定，頁碼放在提示列。
func newLookupTable(candidates, labels []string, cursor int, horizontal ...bool) dbus.Variant {
	t := ibusLookupTable{Name: "IBusLookupTable", Attach: map[string]dbus.Variant{},
		PageSize: uint32(max(len(candidates), 1)), Cursor: uint32(max(cursor, 0)), CursorVisible: true,
		Orientation: orientationVertical, Candidates: []dbus.Variant{}, Labels: []dbus.Variant{}}
	if len(horizontal) > 0 && horizontal[0] {
		t.Orientation = 0
	}
	for _, c := range candidates {
		t.Candidates = append(t.Candidates, newText(c))
	}
	for _, l := range labels {
		t.Labels = append(t.Labels, newText(l))
	}
	return dbus.MakeVariant(t)
}

type property struct {
	key, label, symbol string
	typ                uint32
	sensitive          bool
	hidden             bool // IBusProperty 的 visible 取反：零值是看得到
	checked            bool // propToggle 的勾選狀態
}

func (p property) variant() dbus.Variant {
	return dbus.MakeVariant(ibusProperty{Name: "IBusProperty", Attach: map[string]dbus.Variant{},
		Key: p.key, Type: p.typ, Label: newText(p.label), Tooltip: newText(""), Sensitive: p.sensitive,
		Visible: !p.hidden, State: p.state(), SubProps: newPropList(nil), Symbol: newText(p.symbol)})
}

func (p property) state() uint32 {
	if p.checked {
		return propStateChecked
	}
	return 0
}

func newPropList(props []property) dbus.Variant {
	l := ibusPropList{Name: "IBusPropList", Attach: map[string]dbus.Variant{}, Props: []dbus.Variant{}}
	for _, p := range props {
		l.Props = append(l.Props, p.variant())
	}
	return dbus.MakeVariant(l)
}

// textOf 取出 IBusText variant 的文字（SetSurroundingText 的參數）。
func textOf(v dbus.Variant) (string, bool) {
	if t, ok := v.Value().(ibusText); ok {
		return t.Text, true
	}
	fields, ok := v.Value().([]interface{})
	if !ok || len(fields) < 3 {
		return "", false
	}
	if name, _ := fields[0].(string); name != "IBusText" {
		return "", false
	}
	s, ok := fields[2].(string)
	return s, ok
}

const engineIntrospection = `<node>
  <interface name="org.freedesktop.IBus.Engine">
    <method name="ProcessKeyEvent"><arg direction="in" type="u"/><arg direction="in" type="u"/><arg direction="in" type="u"/><arg direction="out" type="b"/></method>
    <method name="SetCursorLocation"><arg direction="in" type="i"/><arg direction="in" type="i"/><arg direction="in" type="i"/><arg direction="in" type="i"/></method>
    <method name="SetCapabilities"><arg direction="in" type="u"/></method>
    <method name="PropertyActivate"><arg direction="in" type="s"/><arg direction="in" type="u"/></method>
    <method name="CandidateClicked"><arg direction="in" type="u"/><arg direction="in" type="u"/><arg direction="in" type="u"/></method>
    <method name="FocusIn"/>
    <method name="FocusOut"/>
    <method name="Reset"/>
    <method name="Enable"/>
    <method name="Disable"/>
    <method name="PageUp"/>
    <method name="PageDown"/>
    <method name="CursorUp"/>
    <method name="CursorDown"/>
    <method name="SetSurroundingText"><arg direction="in" type="v"/><arg direction="in" type="u"/><arg direction="in" type="u"/></method>
    <signal name="CommitText"><arg type="v"/></signal>
    <signal name="UpdatePreeditText"><arg type="v"/><arg type="u"/><arg type="b"/><arg type="u"/></signal>
    <signal name="UpdateAuxiliaryText"><arg type="v"/><arg type="b"/></signal>
    <signal name="UpdateLookupTable"><arg type="v"/><arg type="b"/></signal>
    <signal name="RegisterProperties"><arg type="v"/></signal>
    <signal name="UpdateProperty"><arg type="v"/></signal>
    <property name="ContentType" type="(uu)" access="write"/>
    <property name="FocusId" type="(b)" access="read"/>
    <property name="ActiveSurroundingText" type="(b)" access="read"/>
  </interface>
  <interface name="org.freedesktop.IBus.Service"><method name="Destroy"/></interface>
</node>`

const factoryIntrospection = `<node>
  <interface name="org.freedesktop.IBus.Factory">
    <method name="CreateEngine"><arg direction="in" type="s"/><arg direction="out" type="o"/></method>
  </interface>
</node>`
