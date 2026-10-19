#!/usr/bin/env python3
"""由原始碼樹或固定 Git 提交與已校驗的大型輸入重建公開系統字典。

0c-compile 以公開來源檢查編譯詞庫、詞頻與詞對統計，0c-english 產生英文子包；
產物使用格式 2 及跨提交前文權重，須通過 0c-cli 冒煙與單字排序閘門（0c-single-gate）才產生輸出。
此為維護者更新字典的工具，須以 --bigram-dir 提供符合 lock 的外部統計檔；一般建置直接使用 assets/data/。
資料 manifest 變更後，模型仍須重新確認系統字典配對，見 models/README.md。
"""
import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile

import release_inputs
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

COMPILE_ARGS = [
    "-public=true", "-relative-readings=true", "-inherit=true",
    "-yibu=true", "-neutral=true", "-polyphone=true", "-max-variants=4",
    "-unigram=mcbopomofo=2,0c_collect=1", "-unigram-fallback=chewing-v4,zhwiki",
    "-fallback-scale=0.25", "-alpha=0.5", "-prune-mu=0.7",
    "-prune-by=entropy",
    "-demote=", "-rare=cns11643,mcbopomofo-rare",
    "-bigram=chewing-v4=3,zhwiki=1,0c_collect-ns=3,0c_collect-ss=2", "-bigram-max=12000000",
]
BOOST = ""

RUNTIME = {"version": 1, "document_context_weight": 0.075}


def sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def json_bytes(value):
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode("utf-8")


def run(command, log, **kwargs):
    log.write(("$ " + " ".join(str(c) for c in command) + "\n").encode())
    log.flush()
    subprocess.run([str(c) for c in command], stdout=log, stderr=subprocess.STDOUT, check=True, **kwargs)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--out", type=Path, required=True, help="新的輸出目錄（產生 <out>/data 與 <out>/build-record.json）")
    p.add_argument("--rev", help="取指定 Git 提交；省略時使用目前的原始碼樹，亦適用解壓後的 tarball")
    p.add_argument("--bigram-dir", type=Path, help="含 chewing-v4.tsv、zhwiki.tsv、0c_collect-ns.tsv、0c_collect-ss.tsv")
    p.add_argument("--boost", default=BOOST, help=f"0c-compile -boost（預設 {BOOST!r}）")
    p.add_argument("--go", default="go")
    p.add_argument("--models", type=Path, help="同時將固定公開模型配對至重建字典，輸出 <out>/models")
    a = p.parse_args()
    compile_args = COMPILE_ARGS + ["-boost=" + a.boost]
    out = a.out.absolute()
    if out.exists():
        p.error(f"輸出已存在：{out}")
    rev = subprocess.check_output(["git", "rev-parse", "--verify", a.rev + "^{commit}"], cwd=ROOT, text=True).strip() if a.rev else None
    out.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".release-data-", dir=out.parent) as temp:
        work = Path(temp)
        src = work / "src"
        src.mkdir()
        source_paths = ("go.mod", "go.sum", "core", "cmd", "internal", "bridge", "data", "models")
        if rev:
            archive = subprocess.run(["git", "archive", "--format=tar", rev, *source_paths],
                                     cwd=ROOT, check=True, capture_output=True).stdout
            subprocess.run(["tar", "-x", "-C", str(src)], input=archive, check=True)
        else:
            for name in source_paths:
                source = ROOT / name
                if name == "models":
                    (src / name).mkdir()
                    for lockfile in source.glob("*.lock.json"):
                        shutil.copyfile(lockfile, src / name / lockfile.name)
                elif source.is_dir():
                    shutil.copytree(source, src / name, ignore=shutil.ignore_patterns("bigram", "raw", "research", "__pycache__"))
                else:
                    shutil.copyfile(source, src / name)
        lock = release_inputs.read_lock(src)
        bigram_dir = a.bigram_dir or ROOT / release_inputs.DIRECTORY
        release_inputs.check(bigram_dir, lock)
        (src / release_inputs.DIRECTORY).mkdir(parents=True, exist_ok=True)
        for name in lock["files"]:
            shutil.copyfile(bigram_dir / name, src / release_inputs.DIRECTORY / name)
        release_inputs.check(src / release_inputs.DIRECTORY, lock)
        env = dict(os.environ, GOWORK="off", GOFLAGS="-mod=readonly", GOTOOLCHAIN="local")
        with open(work / "build.log", "wb") as log:
            try:
                for tool in ("0c-compile", "0c-english", "0c-cli", "0c-single-gate"):
                    run([a.go, "build", "-trimpath", "-o", work / tool, "./cmd/" + tool], log, cwd=src, env=env)
                base = work / "base"
                run([work / "0c-compile", "-data", src / "data", "-out", base, *compile_args], log, cwd=src)
                run([work / "0c-english", "-source", src / "data/english", "-out", base / "english"], log, cwd=src)
                mixed = work / "mix" / "data"
                (work / "mix").mkdir()
                shutil.copytree(base, mixed)
                manifest = json.loads((mixed / "manifest.json").read_text(encoding="utf-8"))
                manifest["format"] = 2
                manifest["runtime"] = RUNTIME
                (mixed / "manifest.json").write_bytes(json_bytes(manifest))
                run([work / "0c-cli", "-data", mixed], log, cwd=src, stdin=subprocess.DEVNULL)
                # 單字排序閘門（#172）：非常用字不得在常見前文下排到詞頻高 10 倍的同音常用字前面；
                # 出現不在核可名單的字就停止，人工確認後以 0c-single-gate -write 更新名單。
                run([work / "0c-single-gate", "-data", mixed, "-source", src / "data",
                     "-approved", src / "data/curation/single-gate.tsv"], log, cwd=src)
                if a.models:
                    from pair_models import export
                    export(src, mixed, a.models.resolve(), work / "mix/models")
            except subprocess.CalledProcessError:
                sys.stderr.write((work / "build.log").read_text(encoding="utf-8", errors="replace")[-8192:])
                raise SystemExit("建置失敗")
        record = {
            "revision": rev,
            "go": subprocess.check_output([a.go, "version"], text=True).strip(),
            "compile_args": compile_args,
            "runtime": RUNTIME,
            "inputs": {"bigram/" + k: v["sha256"] for k, v in lock["files"].items()},
            "base_manifest_sha256": sha(base / "manifest.json"),
            "files": {str(f.relative_to(mixed)): sha(f) for f in sorted(mixed.rglob("*")) if f.is_file()},
        }
        out.mkdir()
        shutil.move(str(mixed), out / "data")
        if a.models:
            shutil.move(str(work / "mix/models"), out / "models")
        shutil.move(str(work / "build.log"), out / "build.log")
        (out / "build-record.json").write_bytes(json_bytes(record))
    print(json.dumps({"out": str(out / "data"), "manifest_sha256": record["files"]["manifest.json"],
                      "lexicon.bin": record["files"]["lexicon.bin"], "lm.bin": record["files"]["lm.bin"]}, indent=2))


if __name__ == "__main__":
    main()
