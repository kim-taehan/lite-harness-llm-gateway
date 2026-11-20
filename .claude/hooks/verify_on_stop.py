#!/usr/bin/env python3
"""Stop 훅. 소스가 바뀌었으면 gofmt·vet·빌드·테스트(모듈 경계 테스트 포함)를 돌리고, 실패하면 작업을 끝내지 못하게 한다.

- 마지막으로 통과한 소스 상태와 같으면 다시 돌리지 않는다(.claude/.cache/verified)
- 같은 실패로 연속 3번 막았으면 더 막지 않고 사용자에게 넘긴다(무한 반복 방지)
"""
import hashlib
import json
import os
import subprocess
import sys

WATCHED = ("cmd", "internal", "go.mod", "go.sum")
MAX_BLOCKS = 3


def main():
    event = json.load(sys.stdin)
    project = os.environ.get("CLAUDE_PROJECT_DIR") or event.get("cwd") or os.getcwd()
    cache = os.path.join(project, ".claude", ".cache")
    os.makedirs(cache, exist_ok=True)
    verified_file = os.path.join(cache, "verified")
    blocks_file = os.path.join(cache, "stop_blocks")

    state = source_state(project)
    if read(verified_file) == state:
        write(blocks_file, "0")
        return 0

    ok, output = run_checks(project)
    if ok:
        write(verified_file, state)
        write(blocks_file, "0")
        return 0

    blocks = int(read(blocks_file) or "0") + 1
    write(blocks_file, str(blocks))
    if blocks > MAX_BLOCKS:
        write(blocks_file, "0")
        print(json.dumps({"systemMessage": "검증 훅: gofmt·vet·테스트가 계속 실패한다. 사용자 확인이 필요하다."}))
        return 0

    sys.stderr.write(
        "검증 훅: 작업을 끝내기 전에 고쳐야 한다(gofmt·vet·테스트, 모듈 경계 테스트 포함).\n"
        + tail(output, 60)
        + "\n고친 뒤 끝내면 다시 검사한다. 원인이 훅이나 규칙 쪽이라고 판단되면 사용자에게 알린다.\n"
    )
    return 2


def source_state(project):
    """추적 중인 변경(diff)과 추적되지 않은 파일 내용을 합친 해시."""
    git = ["git", "-C", project]
    diff = subprocess.run(git + ["diff", "HEAD", "--", *WATCHED], capture_output=True).stdout
    untracked = subprocess.run(
        git + ["ls-files", "--others", "--exclude-standard", "--", *WATCHED], capture_output=True, text=True
    ).stdout.split()
    digest = hashlib.sha256(diff)
    for rel in sorted(untracked):
        digest.update(rel.encode())
        try:
            with open(os.path.join(project, rel), "rb") as f:
                digest.update(f.read())
        except OSError:
            pass
    return digest.hexdigest()


def run_checks(project):
    env = dict(os.environ)
    # Claude Code 의 셸에는 go 가 PATH 에 없을 수 있다(Homebrew 설치)
    env["PATH"] = os.pathsep.join(["/opt/homebrew/bin", "/usr/local/go/bin", os.path.expanduser("~/go/bin"), env.get("PATH", "")])
    steps = [
        ("gofmt", ["gofmt", "-l", "cmd", "internal"]),
        ("vet", ["go", "vet", "./..."]),
        ("test", ["go", "test", "-count=1", "./..."]),
    ]
    for name, command in steps:
        result = subprocess.run(command, cwd=project, capture_output=True, text=True, env=env, timeout=540)
        output = (result.stdout + result.stderr).strip()
        failed = result.returncode != 0 or (name == "gofmt" and output)
        if failed:
            hint = "\n(gofmt -w 로 고친다)" if name == "gofmt" else ""
            return False, f"[{name}]\n{output}{hint}"
    return True, ""


def tail(text, lines):
    return "\n".join(text.strip().splitlines()[-lines:])


def read(path):
    try:
        with open(path) as f:
            return f.read().strip()
    except OSError:
        return None


def write(path, value):
    with open(path, "w") as f:
        f.write(value)


if __name__ == "__main__":
    sys.exit(main())
