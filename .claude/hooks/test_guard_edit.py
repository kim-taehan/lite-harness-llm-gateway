#!/usr/bin/env python3
"""guard_edit.py 동작 테스트. 실행: python3 .claude/hooks/test_guard_edit.py"""
import json
import os
import subprocess
import sys
import tempfile
import unittest

HOOK = os.path.join(os.path.dirname(os.path.abspath(__file__)), "guard_edit.py")
PROJECT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def run(tool_name, rel, **tool_input):
    tool_input["file_path"] = os.path.join(PROJECT, rel)
    event = {"tool_name": tool_name, "tool_input": tool_input, "cwd": PROJECT}
    result = subprocess.run(
        [sys.executable, HOOK], input=json.dumps(event), capture_output=True, text=True,
        env={**os.environ, "CLAUDE_PROJECT_DIR": PROJECT},
    )
    return result.returncode, result.stderr


def go(package, imports=(), body=""):
    block = "".join(f'\t"{i}"\n' for i in imports)
    import_section = f"import (\n{block})\n\n" if imports else ""
    return f"package {package}\n\n{import_section}{body}\n"


class GuardEditTest(unittest.TestCase):

    def assertBlocked(self, result, phrase):
        code, err = result
        self.assertEqual(2, code, err)
        self.assertIn(phrase, err)

    def assertAllowed(self, result):
        code, err = result
        self.assertEqual(0, code, err)

    # 모듈 경계
    def test_거꾸로_된_의존을_막는다(self):
        src = go("core", ["llm-gateway/internal/auth"])
        self.assertBlocked(run("Write", "internal/core/x.go", content=src), "`core` → `auth`")

    def test_auth_와_limits_는_서로_기대지_않는다(self):
        src = go("auth", ["llm-gateway/internal/limits"])
        self.assertBlocked(run("Write", "internal/auth/x.go", content=src), "`auth` → `limits`")

    def test_허용된_방향은_통과한다(self):
        src = go("admin", ["context", "llm-gateway/internal/auth", "llm-gateway/internal/core"])
        self.assertAllowed(run("Write", "internal/admin/x.go", content=src))

    def test_같은_모듈의_하위_패키지는_통과한다(self):
        src = go("store", ["llm-gateway/internal/auth"])
        self.assertAllowed(run("Write", "internal/auth/store/x.go", content=src))

    def test_cmd_는_app_만_가져온다(self):
        src = go("main", ["llm-gateway/internal/auth"])
        self.assertBlocked(run("Write", "cmd/llm-gateway/main.go", content=src), "internal/app")

    def test_설계에_없는_모듈을_막는다(self):
        self.assertBlocked(run("Write", "internal/billing/x.go", content=go("billing")), "설계에 없다")

    def test_뭉뚱그린_패키지_이름을_막는다(self):
        self.assertBlocked(run("Write", "internal/core/util/x.go", content=go("util")), "`util`")

    # Go 관례
    def test_조립_지점_밖의_context_Background_를_막는다(self):
        src = go("auth", ["context"], "func f() { _ = context.Background() }")
        self.assertBlocked(run("Write", "internal/auth/x.go", content=src), "context.Background")
        self.assertAllowed(run("Write", "internal/app/x.go", content=go("app", ["context"], "func f() { _ = context.Background() }")))

    def test_time_Sleep_와_기본_HTTP_클라이언트를_막는다(self):
        self.assertBlocked(run("Write", "internal/core/x.go", content=go("core", ["time"], "func f() { time.Sleep(1) }")), "time.Sleep")
        self.assertBlocked(run("Write", "internal/core/x.go", content=go("core", ["net/http"], "func f() { http.Get(u) }")), "타임아웃")

    def test_표준_log_를_막는다(self):
        self.assertBlocked(run("Write", "internal/core/x.go", content=go("core", ["log"])), "log/slog")
        self.assertAllowed(run("Write", "internal/core/x.go", content=go("core", ["log/slog"])))

    def test_테스트_파일은_관례_검사를_하지_않는다(self):
        src = go("auth", ["context", "time"], "func f() { _ = context.Background(); time.Sleep(1) }")
        self.assertAllowed(run("Write", "internal/auth/x_test.go", content=src))

    def test_테스트_파일도_의존_방향은_검사한다(self):
        src = go("core", ["llm-gateway/internal/auth"])
        self.assertBlocked(run("Write", "internal/core/x_test.go", content=src), "`core` → `auth`")

    def test_문자열과_주석_안의_패턴은_무시한다(self):
        src = go("core", body='// time.Sleep(1)\nvar s = "context.Background() time.Sleep("\nvar r = `http.Get(`')
        self.assertAllowed(run("Write", "internal/core/x.go", content=src))

    def test_외부_주소를_막고_내부_주소는_통과한다(self):
        self.assertBlocked(run("Write", "internal/core/x.go", content=go("core", body='var u = "https://api.openai.com/v1"')), "외부 주소")
        self.assertAllowed(run("Write", "internal/core/x.go", content=go("core", body='var u = "http://localhost:11434"')))

    # Edit 은 적용 결과 전체로 검사한다
    def test_Edit_결과로_검사한다(self):
        with tempfile.TemporaryDirectory(dir=os.path.join(PROJECT, "internal/core")) as d:
            rel = os.path.relpath(os.path.join(d, "x.go"), PROJECT)
            with open(os.path.join(PROJECT, rel), "w") as f:
                f.write(go("x", ["time"], "func f() { _ = 1 }"))
            self.assertBlocked(run("Edit", rel, old_string="_ = 1", new_string="time.Sleep(1)"), "time.Sleep")
            self.assertAllowed(run("Edit", rel, old_string="_ = 1", new_string="_ = 2"))

    # 마이그레이션
    def test_마이그레이션은_스키마만_바꾼다(self):
        rel = "internal/core/migrations/0099_seed.sql"
        self.assertBlocked(run("Write", rel, content="INSERT INTO team VALUES (1);"), "스키마만")
        self.assertAllowed(run("Write", rel, content="CREATE TABLE t (id text primary key);"))

    def test_마이그레이션_파일_이름_규칙(self):
        self.assertBlocked(run("Write", "internal/core/migrations/create_team.sql", content="CREATE TABLE t (id int);"), "4자리 번호")

    def test_규칙_밖의_파일은_통과한다(self):
        self.assertAllowed(run("Write", "GATEWAY_DESIGN.md", content="time.Sleep context.Background()"))


if __name__ == "__main__":
    unittest.main(verbosity=1)
