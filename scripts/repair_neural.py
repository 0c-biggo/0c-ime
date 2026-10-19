#!/usr/bin/env python3
"""把已配對舊字典的 neural/ 複製一份，改成配對新字典（只改配對雜湊，不動模型）。

引擎與 worker 只以字典 manifest.json 整檔的 SHA-256 核對配對（選字模型 manifest.json／vocab.json、
補標點 vocab.json 的 dictionary_sha256，模型 manifest 的 files 記 vocab.json 雜湊）。模型輸入只有
注音音節（bases）、聲調與字元（characters）；字典只決定候選，不在 vocab 的候選字由 runtime 整段退回
統計引擎（manifest limits.unknown_candidate_character）。所以只改詞條讀音、不換模型的字典修正，可以把
同一個模型重新配對到新字典。公開發佈使用 pair_models.py 的固定配對清單；打包不修改來源紀錄。

條件（任一不成立就拒絕、不寫檔）：
- 來源 neural/ 確實配對 --old-data 的字典（含模型 manifest 記的 vocab.json 雜湊）；
- 新舊字典的 format、runtime 相同（前文權重是和模型一起校準的）；
- 新字典新增的單字讀音所用的字與音節都在模型 vocab 內（--expect-char／--expect-base 列出，逐一核對）。

換模型或字典有大幅變動（重新匯入、改 bigram）時不要用這支，照 platforms/neural/README.md 重新產生。
"""
import argparse
import hashlib
import json
import shutil
import sys
from pathlib import Path

KINDS = ("select", "punct")


def sha_bytes(data):
    return hashlib.sha256(data).hexdigest()


def sha(path):
    return sha_bytes(Path(path).read_bytes())


def json_bytes(value):
    # 與 0c-compile／模型匯出／package_release.py 相同的格式。
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode("utf-8")


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--neural", type=Path, required=True, help="已配對舊字典的 neural/ 目錄")
    p.add_argument("--old-data", type=Path, required=True, help="舊字典目錄（neural/ 目前配對的 manifest.json 所在）")
    p.add_argument("--data", type=Path, required=True, help="新字典目錄")
    p.add_argument("--out", type=Path, required=True, help="新的 neural/ 輸出目錄（必須不存在）")
    p.add_argument("--expect-char", action="append", default=[], help="新字典新增讀音的字，必須在模型 vocab 內")
    p.add_argument("--expect-base", action="append", default=[], help="新字典新增讀音的音節（不含聲調），必須在模型 vocab 內")
    a = p.parse_args()
    if a.out.exists():
        p.error(f"輸出已存在：{a.out}")
    old_manifest = json.loads((a.old_data / "manifest.json").read_text(encoding="utf-8"))
    new_manifest = json.loads((a.data / "manifest.json").read_text(encoding="utf-8"))
    old, new = sha(a.old_data / "manifest.json"), sha(a.data / "manifest.json")
    for key in ("format", "runtime"):
        if old_manifest.get(key) != new_manifest.get(key):
            raise SystemExit(f"新舊字典的 {key} 不同，不能只改配對")
    for name in ("lexicon.bin", "lm.bin"):
        info = new_manifest["files"][name]
        if sha(a.data / name) != info["sha256"] or (a.data / name).stat().st_size != info["size"]:
            raise SystemExit(f"新字典 {name} 與 manifest 不符")
    packages = {}
    for kind in KINDS:
        if not (a.neural / kind / "manifest.json").exists():
            if kind == "select":
                raise SystemExit("缺少選字模型")
            continue
        manifest = json.loads((a.neural / kind / "manifest.json").read_text(encoding="utf-8"))
        vocab = json.loads((a.neural / kind / "vocab.json").read_text(encoding="utf-8"))
        if manifest.get("files", {}).get("vocab.json") != sha(a.neural / kind / "vocab.json"):
            raise SystemExit(f"{kind} 的 vocab.json 與模型 manifest 不符")
        if manifest.get("files", {}).get("model.onnx") != sha(a.neural / kind / "model.onnx"):
            raise SystemExit(f"{kind} 的 model.onnx 與模型 manifest 不符")
        recorded = [m["dictionary_sha256"] for m in (manifest, vocab) if "dictionary_sha256" in m]
        if not recorded or (kind == "select" and len(recorded) != 2) or any(r != old for r in recorded):
            raise SystemExit(f"{kind} 沒有配對舊字典 {old}")
        missing = [c for c in a.expect_char if c not in vocab.get("characters", [])]
        missing += [b for b in a.expect_base if "bases" in vocab and b not in vocab["bases"]]
        if missing:
            raise SystemExit(f"{kind} 的 vocab 沒有 {missing}，模型看不到新讀音，要重新訓練或重新產生")
        packages[kind] = (manifest, vocab)
    shutil.copytree(a.neural, a.out, symlinks=True)
    for kind, (manifest, vocab) in packages.items():
        if "dictionary_sha256" in vocab:
            vocab["dictionary_sha256"] = new
        vocab_bytes = json_bytes(vocab)
        if "dictionary_sha256" in manifest:
            manifest["dictionary_sha256"] = new
        manifest["files"]["vocab.json"] = sha_bytes(vocab_bytes)
        (a.out / kind / "vocab.json").write_bytes(vocab_bytes)
        (a.out / kind / "manifest.json").write_bytes(json_bytes(manifest))
    json.dump({"from_dictionary_sha256": old, "to_dictionary_sha256": new, "packages": sorted(packages),
               "select_manifest_sha256": sha(a.out / "select/manifest.json")}, sys.stdout, indent=2)
    print()


if __name__ == "__main__":
    main()
