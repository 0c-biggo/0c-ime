package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

type profileOptions struct {
	action, id, name string
	apply, json      bool
}

func parseProfileArgs(args []string) (profileOptions, error) {
	var o profileOptions
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-json", "--json":
			if o.json {
				return o, usageError("重複參數：" + a)
			}
			o.json = true
		case "-apply", "--apply":
			if o.apply {
				return o, usageError("重複參數：" + a)
			}
			o.apply = true
		case "-name", "--name":
			if o.name != "" || i+1 >= len(args) {
				return o, usageError("請指定一個風格名稱")
			}
			i++
			o.name = args[i]
		default:
			if strings.HasPrefix(a, "-") {
				return o, usageError("未知參數：" + a)
			}
			positional = append(positional, a)
		}
	}
	if len(positional) != 2 {
		return o, usageError("用法：0c-update profile select|copy|rename|delete <id> [--name 名稱] [-apply] [-json]")
	}
	o.action, o.id = positional[0], positional[1]
	switch o.action {
	case "copy", "rename":
		if strings.TrimSpace(o.name) == "" {
			return o, usageError("請指定 --name 名稱")
		}
	case "select", "delete":
		if o.name != "" {
			return o, usageError("此操作不接受名稱")
		}
	default:
		return o, usageError("未知風格操作：" + o.action)
	}
	if o.apply && o.action != "select" && o.action != "copy" {
		return o, usageError("只有選用或複製風格能套用到引擎")
	}
	return o, nil
}

type profileResult struct {
	Schema   int                  `json:"schema"`
	OK       bool                 `json:"ok"`
	Apply    applyView            `json:"apply"`
	Profiles settings.ProfileView `json:"profiles"`
}

func runProfile(base string, dial autoupdate.Dialer, o profileOptions) (profileResult, error) {
	res := profileResult{Schema: contractSchema, OK: true, Apply: applyView{State: applyNotRequested}}
	err := autoupdate.WithLock(base, settings.WriteLock, func() error {
		path := settings.Path(filepath.Join(base, "user.json"))
		r, err := settings.Read(path)
		if err != nil {
			return err
		}
		s := r.Settings
		if err = s.ProfileAction(o.action, o.id, o.name); err != nil {
			return err
		}
		if err = settings.Save(path, s); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	if o.apply {
		res.Apply = applySettings(dial, func(string) {})
		ensureEngineService(base)
	}
	res.Profiles = buildSettingsDoc(base, dial).Profiles
	return res, nil
}
func profileCommand(args []string) error {
	o, err := parseProfileArgs(args)
	asJSON := false
	for _, a := range args {
		asJSON = asJSON || isJSONFlag(a)
	}
	if err != nil {
		if asJSON {
			return failJSON(os.Stdout, err)
		}
		return err
	}
	res, err := runProfile(basePath(), dialEngine, o)
	if err != nil {
		if o.json {
			return failJSON(os.Stdout, err)
		}
		return err
	}
	if o.json {
		return emitJSON(os.Stdout, res)
	}
	fmt.Fprintf(os.Stdout, "輸入習慣已儲存：%s（%s）\n", res.Profiles.Active, res.Apply.State)
	return nil
}
