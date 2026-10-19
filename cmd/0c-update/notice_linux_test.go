//go:build !windows && !darwin

package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"git.biggo.com/0c/ime/internal/autoupdate"
	"github.com/godbus/dbus/v5"
)

type noticeFixture struct {
	reject bool
	calls  chan dbus.Sender
}

func (f *noticeFixture) Notify(sender dbus.Sender, app string, replace uint32, icon, title, body string, actions []string, hints map[string]dbus.Variant, expire int32) (uint32, *dbus.Error) {
	f.calls <- sender
	if f.reject {
		return 0, dbus.NewError("org.freedesktop.DBus.Error.Failed", []interface{}{"delivery rejected"})
	}
	return 91, nil
}

func TestNoticeDeliveryHelper(t *testing.T) {
	if os.Getenv("ZC_NOTICE_TEST_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	if os.Getenv("ZC_NOTICE_TEST_STALL") == "1" {
		time.Sleep(time.Minute)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	action, err := notifyWithAction(ctx, autoupdate.Notice{Title: "test", Body: "private D-Bus", Action: "finish"})
	if err != nil {
		os.Exit(3)
	}
	if action != "later" {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestNoticeDeliveryAcknowledgement(t *testing.T) {
	for _, reject := range []bool{true, false} {
		name := "accepted"
		if reject {
			name = "rejected"
		}
		t.Run(name, func(t *testing.T) {
			daemon := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
			stdout, err := daemon.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = daemon.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { daemon.Process.Kill(); daemon.Wait() }()
			address, err := bufio.NewReader(stdout).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("DBUS_SESSION_BUS_ADDRESS", strings.TrimSpace(address))
			server, err := dbus.ConnectSessionBus()
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			f := &noticeFixture{reject: reject, calls: make(chan dbus.Sender, 1)}
			if err = server.Export(f, notifyPath, notifyName); err != nil {
				t.Fatal(err)
			}
			if _, err = server.RequestName(notifyName, dbus.NameFlagDoNotQueue); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestNoticeDeliveryHelper$")
			cmd.Env = append(os.Environ(), "ZC_NOTICE_TEST_HELPER=1")
			err = startNoticeHelper(cmd, 2*time.Second)
			if reject {
				if err == nil {
					t.Fatal("rejected delivery reported success")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Delivery must return before any click. Keep the original connection for actions.
			sender := <-f.calls
			if err = server.Send(&dbus.Message{Type: dbus.TypeSignal, Headers: map[dbus.HeaderField]dbus.Variant{dbus.FieldPath: dbus.MakeVariant(dbus.ObjectPath(notifyPath)), dbus.FieldInterface: dbus.MakeVariant(notifyName), dbus.FieldMember: dbus.MakeVariant("ActionInvoked"), dbus.FieldDestination: dbus.MakeVariant(string(sender)), dbus.FieldSignature: dbus.MakeVariant(dbus.SignatureOf(uint32(91), "later"))}, Body: []interface{}{uint32(91), "later"}}, nil).Err; err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				var owner bool
				if e := server.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, string(sender)).Store(&owner); e != nil {
					t.Fatal(e)
				}
				if !owner {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("helper did not finish after action")
		})
	}
}

func TestNoticeDeliveryHelperTimeoutAndExit(t *testing.T) {
	for _, stall := range []bool{false, true} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestNoticeDeliveryHelper$")
		cmd.Env = append(os.Environ(), "ZC_NOTICE_TEST_HELPER=1", "DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent")
		if stall {
			cmd.Env = append(cmd.Env, "ZC_NOTICE_TEST_STALL=1")
		}
		if err := startNoticeHelper(cmd, 100*time.Millisecond); err == nil {
			t.Fatal("helper exit/timeout reported success")
		}
		if cmd.ProcessState == nil {
			t.Fatal("failed helper not reaped")
		}
	}
}
