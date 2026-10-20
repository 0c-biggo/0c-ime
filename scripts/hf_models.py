#!/usr/bin/env python3
"""官網套件內建的神經模型：取得 Hugging Face 原檔、配對到出貨字典、核對出貨結果（#180）。

0.2.3 起官網三平台套件內建的 neural/select、neural/punct 就是 models/models.lock.json 記的 Hugging Face 公開版
（與 Linux 系統安裝讓使用者下載的同一份），不再用內部匯出。做法：

- 權重：model.onnx、LICENSE、README.md 從 --from 指定的目錄（<dir>/<select|punct>/<檔名>，例如 0c-ime 的 models/）、
  快取（build/cache/models/<role>/<revision>/）或 Hugging Face（<repo>/resolve/<revision>/<檔名>）取得，逐檔核對
  models.lock 的大小與 SHA-256。
- 配對：manifest.json、vocab.json 用 repo 裡的 models/upstream（同樣先核對 models.lock），依 pairing_lock.py 的
  metadata()（與 0c-ime scripts/pair_models.py 相同）只改 dictionary_sha256 與 manifest 記的 vocab 雜湊，權重不動。
  結果必須等於這份字典的 pairing.lock.json。
- 核對（check）：出貨的 neural/ 兩個模型的 manifest.json、vocab.json 等於出貨字典的 pairing.lock，model.onnx（與
  LICENSE、README.md）等於 models.lock。公開包打包時（package_release／package_windows／package_macos）自動執行。

0.2.2 以前官網套件內建的 model.onnx（cef689f1…、2d98c7c6…）與公開版權重相同、只差匯出資訊，記在
models/equivalent.json；model_identity() 兩種都認，給比較新舊套件的發版檢查用。發版出貨的一律是公開版原檔。

用法：
  python3 -B scripts/hf_models.py fetch [--from DIR]                                  # 取得並核對原檔
  python3 -B scripts/hf_models.py pair --data <字典> --out <目錄> [--from DIR]          # 產生配對好的 select/、punct/
  python3 -B scripts/hf_models.py neural --neural <現有 neural/> --data <字典> --out <新 neural/> [--from DIR] [--expect-char 字]
  python3 -B scripts/hf_models.py check --neural <neural/> --data <出貨字典>
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
import urllib.request

from pairing_lock import LOCK, ROLES, UPSTREAM, metadata, pairing_lock

ROOT = Path(__file__).resolve().parents[1]
EQUIVALENT = ROOT / "models/equivalent.json"
CACHE = ROOT / "build/cache/models"
HF = "https://huggingface.co"
METADATA = ("manifest.json", "vocab.json")  # 由 models/upstream 配對產生，不從權重來源拿


def digest(path):
    h = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def load_lock(path=LOCK):
    lock = json.loads(Path(path).read_bytes())
    if lock.get("schema") != 1 or set(lock.get("models", {})) != set(ROLES):
        raise ValueError("models.lock.json 格式不符")
    return lock


def weight_names(lock, role):
    return sorted(n for n in lock["models"][role]["files"] if n not in METADATA)


def model_identity(role, sha256, lock_path=LOCK, equivalent=EQUIVALENT):
    """model.onnx 的雜湊是不是 models.lock 的上游模型："upstream"、"equivalent"（equivalent.json）或 None。

    與 Go 的 internal/models Lock.Match 相同：等價紀錄只在 repository、revision、model.onnx 都跟 models.lock 一樣時成立。
    """
    lock = load_lock(lock_path)
    equivalent = json.loads(Path(equivalent).read_text(encoding="utf-8"))
    model = lock["models"].get(role)
    if not model or not sha256:
        return None
    want = model["files"]["model.onnx"]["sha256"]
    if sha256 == want:
        return "upstream"
    e = equivalent.get("models", {}).get(role)
    if not e or (e.get("repository"), e.get("revision"), e.get("model.onnx")) != (model["repository"], model["revision"], want):
        return None
    return "equivalent" if any(item.get("sha256") == sha256 for item in e.get("equivalent", [])) else None


def check_file(path, want, label):
    path = Path(path)
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"{label} 不存在或不是一般檔案：{path}")
    if path.stat().st_size != want["size"] or digest(path) != want["sha256"]:
        raise ValueError(f"{label} 與 models.lock 不符：{path}")
    return path


def download(url, target):
    target.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=target.parent, prefix=".part-", delete=False) as out:
        try:
            with urllib.request.urlopen(url, timeout=60) as reply:
                shutil.copyfileobj(reply, out, 1 << 20)
        except BaseException:
            os.unlink(out.name)
            raise
    os.replace(out.name, target)


def fetch(source=None, cache=CACHE, base_url=None, lock_path=LOCK, log=None):
    """回傳 {role: {檔名: 已核對的路徑}}（model.onnx、LICENSE、README.md）。

    有 source 時只用 source（<source>/<role>/<檔名>），不連網；否則用快取，缺的從 Hugging Face（或 base_url 鏡像）下載。
    """
    lock = load_lock(lock_path)
    base = (base_url or os.environ.get("ZC_MODELS_BASE_URL") or HF).rstrip("/")
    out = {}
    for role in ROLES:
        model = lock["models"][role]
        files = {}
        for name in weight_names(lock, role):
            want = model["files"][name]
            label = f"{role}/{name}"
            if source:
                files[name] = check_file(Path(source) / role / name, want, label)
                continue
            target = Path(cache) / role / model["revision"] / name
            if not target.exists() or target.stat().st_size != want["size"] or digest(target) != want["sha256"]:
                url = f"{base}/{model['repository']}/resolve/{model['revision']}/{name}"
                if log:
                    print(f"下載 {url}", file=log)
                download(url, target)
            files[name] = check_file(target, want, label)
        out[role] = files
    return out


def upstream_metadata(lock, upstream=UPSTREAM):
    """models/upstream 的 manifest.json、vocab.json（先核對 models.lock）。"""
    out = {}
    for role in ROLES:
        out[role] = {}
        for name in METADATA:
            path = Path(upstream) / role / name
            check_file(path, lock["models"][role]["files"][name], f"models/upstream/{role}/{name}")
            out[role][name] = json.loads(path.read_bytes())
    return out


def write_packages(weights, dictionary, out, lock_path=LOCK):
    """在 out/<role>/ 寫出配對到 dictionary（字典 manifest 的 SHA-256）的模型；回傳這份字典的 pairing.lock 位元組。"""
    lock = load_lock(lock_path)
    expected = pairing_lock(dictionary, lock_path)
    packages = json.loads(expected)["packages"]
    meta = upstream_metadata(lock)
    for role in ROLES:
        target = Path(out) / role
        target.mkdir(parents=True)
        for name, path in weights[role].items():
            shutil.copyfile(path, target / name)
        derived = metadata(meta[role]["manifest.json"], meta[role]["vocab.json"], dictionary)
        for name, content in derived.items():
            want = packages[role][name]
            if hashlib.sha256(content).hexdigest() != want["sha256"] or len(content) != want["size"]:
                raise ValueError(f"{role}/{name} 配對結果與 pairing.lock 不符")
            (target / name).write_bytes(content)
        for name in weights[role]:  # 複製後再核對一次（磁碟或複製出錯時不出貨）
            check_file(target / name, lock["models"][role]["files"][name], f"{role}/{name}")
    return expected


def missing_in_vocab(chars=(), bases=(), lock_path=LOCK):
    """新字典新增讀音用到的字、音節（不含聲調）不在模型 vocab 裡時回傳它們：模型看不到這些讀音，要換模型。

    同 repair_neural.py 的 --expect-char／--expect-base；不在 vocab 的候選字由 runtime 整段退回統計引擎。
    """
    meta = upstream_metadata(load_lock(lock_path))
    missing = []
    for role in ROLES:
        vocab = meta[role]["vocab.json"]
        missing += [f"{role}:{c}" for c in chars if c not in vocab.get("characters", [])]
        missing += [f"{role}:{b}" for b in bases if "bases" in vocab and b not in vocab["bases"]]
    return missing


def dictionary_of(data):
    return digest(Path(data) / "manifest.json")


def replace_models(neural, data, out, weights, lock_path=LOCK):
    """複製現有的 neural/（可攜 Python、worker），把 select/、punct/ 換成配對到 data 的公開版模型。"""
    neural, out = Path(neural), Path(out)
    if out.exists():
        raise ValueError(f"輸出已存在：{out}")
    if not (neural / "sequence_runtime.py").is_file():
        raise ValueError(f"不是 neural/ 目錄：{neural}")
    if any(p.is_symlink() for p in neural.rglob("*")):
        raise ValueError("神經套件不接受符號連結")
    skip = set(ROLES) | {"pairing.lock.json"}
    out.parent.mkdir(parents=True, exist_ok=True)
    stage = Path(tempfile.mkdtemp(prefix=".neural-", dir=out.parent))
    try:
        for entry in neural.iterdir():
            if entry.name in skip:
                continue
            if entry.is_dir():
                shutil.copytree(entry, stage / entry.name)
            else:
                shutil.copy2(entry, stage / entry.name)
        write_packages(weights, dictionary_of(data), stage, lock_path)
        os.replace(stage, out)
    except BaseException:
        shutil.rmtree(stage, ignore_errors=True)
        raise
    return out


def check_shipped(neural, data, lock_path=LOCK):
    """出貨的 neural/ 是公開版模型、配對 data 的 manifest：manifest.json、vocab.json 等於 pairing.lock，
    model.onnx、LICENSE、README.md 等於 models.lock。回傳這份字典的 pairing.lock 位元組。"""
    lock = load_lock(lock_path)
    neural = Path(neural)
    expected = pairing_lock(dictionary_of(data), lock_path)
    packages = json.loads(expected)["packages"]
    for role in ROLES:
        for name, want in packages[role].items():
            path = neural / role / name
            if not path.is_file() or path.stat().st_size != want["size"] or digest(path) != want["sha256"]:
                raise ValueError(f"neural/{role}/{name} 不是公開版模型配對出貨字典的結果（見 docs/releasing.md 的神經模型）")
        for name in weight_names(lock, role):
            path = neural / role / name
            if name == "model.onnx" or path.exists():
                try:
                    check_file(path, lock["models"][role]["files"][name], f"neural/{role}/{name}")
                except ValueError as error:
                    raise ValueError(f"{error}；官網套件要內建 Hugging Face 公開版（models.lock）") from None
    return expected


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    commands = {name: sub.add_parser(name) for name in ("fetch", "pair", "neural", "check")}
    for name in ("fetch", "pair", "neural"):
        commands[name].add_argument("--from", dest="source", type=Path, help="Hugging Face 原檔目錄（<dir>/<select|punct>/model.onnx…）；省略則用快取或下載")
        commands[name].add_argument("--cache", type=Path, default=CACHE, help="下載快取")
    for name in ("pair", "neural", "check"):
        commands[name].add_argument("--data", type=Path, required=True, help="字典目錄（含 manifest.json）")
    for name in ("pair", "neural"):
        commands[name].add_argument("--expect-char", action="append", default=[], help="新字典新增讀音的字，必須在模型 vocab 內")
        commands[name].add_argument("--expect-base", action="append", default=[], help="新字典新增讀音的音節（不含聲調），必須在模型 vocab 內")
    commands["pair"].add_argument("--out", type=Path, required=True, help="輸出目錄：寫出 select/、punct/ 與 pairing.lock.json（必須不存在）")
    commands["neural"].add_argument("--neural", type=Path, required=True, help="現有的 neural/（沿用它的可攜 Python 與 worker）")
    commands["neural"].add_argument("--out", type=Path, required=True, help="新的 neural/（必須不存在）")
    commands["check"].add_argument("--neural", type=Path, required=True, help="要核對的 neural/")
    args = parser.parse_args()
    try:
        if args.command == "check":
            check_shipped(args.neural, args.data)
            print(f"neural/ 是公開版模型，配對字典 {dictionary_of(args.data)}")
            return
        if args.command in ("pair", "neural"):
            missing = missing_in_vocab(args.expect_char, args.expect_base)
            if missing:
                raise ValueError(f"模型 vocab 沒有 {missing}，模型看不到新讀音，要換模型")
        weights = fetch(args.source, args.cache, log=sys.stderr)
        if args.command == "fetch":
            for role, files in weights.items():
                print(f"{role}：{Path(files['model.onnx']).parent}")
        elif args.command == "pair":
            if args.out.exists():
                raise ValueError(f"輸出已存在：{args.out}")
            args.out.mkdir(parents=True)
            lock_bytes = write_packages(weights, dictionary_of(args.data), args.out)
            (args.out / "pairing.lock.json").write_bytes(lock_bytes)
            print(f"{args.out}：配對字典 {dictionary_of(args.data)}")
        else:
            replace_models(args.neural, args.data, args.out, weights)
            print(json.dumps({"dictionary_sha256": dictionary_of(args.data),
                              "select_manifest_sha256": digest(args.out / "select/manifest.json"),
                              "punct_manifest_sha256": digest(args.out / "punct/manifest.json")}, indent=2))
    except (OSError, ValueError, KeyError) as error:
        parser.exit(1, f"hf_models：{error}\n")


if __name__ == "__main__":
    main()
