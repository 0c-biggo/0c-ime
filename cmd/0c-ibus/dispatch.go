//go:build linux

package main

// godbus 為每個方法呼叫各開一個 goroutine，順序不保證；ibus-daemon 卻可能在上一個按鍵回覆前
// 送出下一個。收訊執行緒依到達順序發號碼牌（WithIncomingInterceptor 是同步的），處理時照號碼輪流，
// 按鍵、失焦與重設因此不會互相超車。

import (
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// orderWait 是等待前一個呼叫的上限；前一個呼叫沒有走到處理函式時不讓後面永遠卡住。
const orderWait = 3 * time.Second

func ours(path dbus.ObjectPath) bool {
	return path == factoryPath || strings.HasPrefix(string(path), enginePrefix)
}

type orderer struct {
	mu              sync.Mutex
	issued, serving uint64
	tickets         map[*dbus.Message]uint64
	changed         chan struct{}
}

func newOrderer() *orderer {
	return &orderer{tickets: map[*dbus.Message]uint64{}, changed: make(chan struct{})}
}

func (o *orderer) intercept(msg *dbus.Message) {
	if msg.Type != dbus.TypeMethodCall {
		return
	}
	path, _ := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	iface, _ := msg.Headers[dbus.FieldInterface].Value().(string)
	if !ours(path) || iface == "org.freedesktop.DBus.Peer" { // Peer 由 godbus 直接回覆，不會進處理函式
		return
	}
	o.mu.Lock()
	o.tickets[msg] = o.issued
	o.issued++
	o.mu.Unlock()
}

// wait 等到輪到這個呼叫；回傳的函式在處理完後呼叫，讓下一個呼叫繼續。
func (o *orderer) wait(msg *dbus.Message) func() {
	o.mu.Lock()
	t, ok := o.tickets[msg]
	delete(o.tickets, msg)
	if !ok {
		o.mu.Unlock()
		return func() {}
	}
	deadline := time.Now().Add(orderWait)
	for o.serving < t {
		ch := o.changed
		o.mu.Unlock()
		select {
		case <-ch:
		case <-time.After(time.Until(deadline)):
		}
		o.mu.Lock()
		if o.serving < t && !time.Now().Before(deadline) {
			o.serving = t
		}
	}
	o.mu.Unlock()
	return func() {
		o.mu.Lock()
		if o.serving <= t {
			o.serving = t + 1
		}
		close(o.changed)
		o.changed = make(chan struct{})
		o.mu.Unlock()
	}
}

// handler 讓 Factory 與所有 engine 路徑的每個介面、方法都進同一個分派函式，
// 每個發了號碼牌的呼叫因此一定會走到 wait。
type handler struct{ s *service }

type object struct {
	s    *service
	path dbus.ObjectPath
}

type iface struct {
	object
	name string
}

type method struct {
	iface
	name string
}

func (h handler) LookupObject(path dbus.ObjectPath) (dbus.ServerObject, bool) {
	if !ours(path) {
		return nil, false
	}
	return object{h.s, path}, true
}

func (o object) LookupInterface(name string) (dbus.Interface, bool) { return iface{o, name}, true }

func (i iface) LookupMethod(name string) (dbus.Method, bool) { return method{i, name}, true }

func (m method) DecodeArguments(_ *dbus.Conn, _ string, msg *dbus.Message, _ []interface{}) ([]interface{}, error) {
	return []interface{}{msg}, nil
}

func (m method) Call(args ...interface{}) ([]interface{}, error) {
	msg := args[0].(*dbus.Message)
	return m.s.dispatch(msg, m.path, m.iface.name, m.name)
}

func (m method) NumArguments() int             { return 1 }
func (m method) NumReturns() int               { return 0 }
func (m method) ArgumentValue(int) interface{} { return nil }
func (m method) ReturnValue(int) interface{}   { return nil }
