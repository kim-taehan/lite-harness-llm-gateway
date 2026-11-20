#!/usr/bin/env python3
"""Stop 훅. 소스가 바뀌었으면 컴파일(경고=오류)과 모듈 경계 테스트를 돌리고, 실패하면 작업을 끝내지 못하게 한다.

- 마지막으로 통과한 소스 상태와 같으면 다시 돌리지 않는다(.claude/.cache/verified)
- 같은 실패로 연속 3번 막았으면 더 막지 않고 사용자에게 넘긴다(무한 반복 방지)
"""
import hashlib
import json
import os
import subprocess
import sys

WATCHED = ("src", "build.gradle.kts", "settings.gradle.kts", "gradle")
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
        print(json.dumps({"systemMessage": "검증 훅: 컴파일·모듈 경계 검사가 계속 실패한다. 사용자 확인이 필요하다."}))
        return 0

    sys.stderr.write(
        "검증 훅: 작업을 끝내기 전에 고쳐야 한다(컴파일 경고=오류, Spring Modulith 경계).\n"
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
    result = subprocess.run(
        ["./gradlew", "--console=plain", "--warn", "compileKotlin", "compileTestKotlin",
         "test", "--tests", "*ModuleStructureTest"],
        cwd=project, capture_output=True, text=True, timeout=540,
    )
    output = "\n".join(
        line for line in (result.stdout + result.stderr).splitlines()
        if not line.startswith("WARNING:") and "Sharing is only supported" not in line
    )
    return result.returncode == 0, output + modulith_violations(project)


def modulith_violations(project):
    """경계 테스트가 실패했으면 테스트 리포트에서 위반 내용을 꺼낸다(gradle 출력에는 요약만 나온다)."""
    import glob
    import html
    import re
    for path in glob.glob(os.path.join(project, "build/test-results/test/*ModuleStructureTest.xml")):
        with open(path, encoding="utf-8") as f:
            m = re.search(r'<failure message="(.*?)"', f.read(), re.S)
        if m:
            return "\n[Modulith 위반]\n" + html.unescape(m.group(1))[:3000]
    return ""


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
